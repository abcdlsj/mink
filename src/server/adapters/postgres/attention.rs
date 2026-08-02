use super::*;

impl PostgresTransaction {
    pub(super) async fn insert_dead_item_notice(
        &mut self,
        agent_id: MemberId,
        thread_id: ThreadId,
        error_code: &'static str,
        now: OffsetDateTime,
    ) -> Result<InboxItemId, ApplicationError> {
        let item_id = InboxItemId::from_uuid(Uuid::now_v7());
        sqlx::query(
            "INSERT INTO inbox_items \
             (id,space_id,member_id,message_id,thread_id,task_id,kind,strength,status,\
              available_at,last_error_code,created_at) \
             SELECT $1,agents.space_id,$2,NULL,$3,NULL,'system','hard','pending',$4,$5,$4 \
             FROM agents WHERE agents.member_id=$2",
        )
        .bind(item_id.into_uuid())
        .bind(agent_id.into_uuid())
        .bind(thread_id.into_uuid())
        .bind(now)
        .bind(error_code)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(item_id)
    }

    /// Writes the Item's lifecycle fields. The ambient Message range is not among them: it changes
    /// only while activity accumulates, which `route_ambient_activity` owns.
    pub(super) async fn record_inbox_item_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        item_id: InboxItemId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO audit_events \
             (id,space_id,actor_member_id,action,subject_type,subject_id,metadata_json,created_at) \
             SELECT $1,i.space_id,$2,$3,'inbox_item',i.id,'{}',$5 \
             FROM inbox_items i WHERE i.id=$4",
        )
        .bind(Uuid::now_v7())
        .bind(actor.into_uuid())
        .bind(action)
        .bind(item_id.into_uuid())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn inbox_item_view(
        &mut self,
        item_id: InboxItemId,
    ) -> Result<InboxItemView, ApplicationError> {
        let row = sqlx::query(
            "SELECT i.id,i.member_id,i.kind,i.strength,i.status,i.available_at,i.created_at,\
                    i.retry_count,i.requeue_count,\
                    i.thread_id,i.message_id,t.channel_id,c.slug AS channel_slug,\
                    m.author_member_id AS sender_member_id,sender.display_name AS sender_name \
             FROM inbox_items i \
             JOIN messages t ON t.id=i.thread_id AND t.placement='root' \
             JOIN channels c ON c.id=t.channel_id \
             LEFT JOIN messages m ON m.id=i.message_id \
             LEFT JOIN members sender ON sender.id=m.author_member_id \
             WHERE i.id=$1",
        )
        .bind(item_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        inbox_view_from_row(&row)
    }

    pub(super) async fn inbox_item(
        &mut self,
        id: InboxItemId,
    ) -> Result<InboxItem, ApplicationError> {
        let row = sqlx::query("SELECT * FROM inbox_items WHERE id = $1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        inbox_from_row(&row)
    }

    pub(super) async fn save_inbox_item(
        &mut self,
        item: InboxItem,
    ) -> Result<(), ApplicationError> {
        let item = item.snapshot();
        let changed = sqlx::query(
            "UPDATE inbox_items SET task_id=$2,status=$3,available_at=$4,lease_run_id=$5, \
             lease_expires_at=$6,retry_count=$7,handled_at=$8,requeue_count=$9, \
             last_error_code=CASE WHEN $3='leased' THEN NULL ELSE last_error_code END WHERE id=$1",
        )
        .bind(item.id.into_uuid())
        .bind(item.task_id.map(TaskId::into_uuid))
        .bind(inbox_status_str(item.status))
        .bind(item.available_at)
        .bind(item.lease_run_id.map(RunId::into_uuid))
        .bind(item.lease_expires_at)
        .bind(i32::try_from(item.retry_count).map_err(|_| ApplicationError::Conflict)?)
        .bind(item.handled_at)
        .bind(i32::try_from(item.requeue_count).map_err(|_| ApplicationError::Conflict)?)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }

    pub(super) async fn inbox_for_member(
        &mut self,
        member_id: MemberId,
        scope: InboxScope,
    ) -> Result<Vec<InboxItemView>, ApplicationError> {
        let statuses: &[&str] = match scope {
            InboxScope::Queue => &["pending", "leased", "deferred"],
            InboxScope::Dead => &["dead"],
        };
        let rows = sqlx::query(
            "SELECT i.id,i.member_id,i.kind,i.strength,i.status,i.available_at,i.created_at,\
                    i.retry_count,i.requeue_count,\
                    i.thread_id,i.message_id,t.channel_id,c.slug AS channel_slug,\
                    m.author_member_id AS sender_member_id,sender.display_name AS sender_name \
             FROM inbox_items i \
             JOIN messages t ON t.id=i.thread_id AND t.placement='root' \
             JOIN channels c ON c.id=t.channel_id \
             LEFT JOIN messages m ON m.id=i.message_id \
             LEFT JOIN members sender ON sender.id=m.author_member_id \
             WHERE i.member_id=$1 AND i.status = ANY($2) \
             ORDER BY i.created_at DESC,i.id DESC",
        )
        .bind(member_id.into_uuid())
        .bind(statuses)
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        rows.iter().map(inbox_view_from_row).collect()
    }

    /// Reads one Item regardless of status, unlike `inbox_for_member`, which projects only the queue.
    /// A governor acting on a retired Item needs to see the result of that action.
    pub(super) async fn route_ambient_activity(
        &mut self,
        space_id: SpaceId,
        member_id: MemberId,
        thread_id: ThreadId,
        kind: InboxItemKind,
        message_seq: u64,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let open = sqlx::query(
            "SELECT id,space_id,member_id,message_id,thread_id,task_id,kind,strength,status,\
                    available_at,lease_run_id,lease_expires_at,retry_count,requeue_count,\
                    handled_at,first_message_seq,last_message_seq,aggregated_count,force_at \
             FROM inbox_items \
             WHERE member_id=$1 AND thread_id=$2 AND strength='ambient' AND status='pending' \
               AND retry_count=0 \
             FOR UPDATE",
        )
        .bind(member_id.into_uuid())
        .bind(thread_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if let Some(row) = open {
            let mut item = inbox_from_row(&row)?;
            // A Message this aggregate already covers is a replay; the range and count stand.
            if item.absorb_ambient_message(message_seq, now).is_err() {
                return Ok(());
            }
            let item_view = item.view();
            let ambient = item_view.ambient.ok_or(ApplicationError::Internal)?;
            sqlx::query(
                "UPDATE inbox_items \
                 SET last_message_seq=$2,aggregated_count=$3,available_at=$4 WHERE id=$1",
            )
            .bind(item_view.id.into_uuid())
            .bind(i64::try_from(ambient.last_message_seq).map_err(|_| ApplicationError::Internal)?)
            .bind(i32::try_from(ambient.aggregated_count).map_err(|_| ApplicationError::Internal)?)
            .bind(item_view.available_at)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
            return Ok(());
        }
        let item = InboxItem::open_ambient(
            InboxItemId::from_uuid(Uuid::now_v7()),
            space_id,
            member_id,
            thread_id,
            kind,
            message_seq,
            now,
        )?;
        let item_view = item.view();
        let ambient = item_view.ambient.ok_or(ApplicationError::Internal)?;
        sqlx::query(
            "INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,task_id,kind,\
             strength,status,available_at,first_message_seq,last_message_seq,aggregated_count,\
             force_at,created_at) \
             VALUES($1,$2,$3,NULL,$4,NULL,$5,'ambient','pending',$6,$7,$7,1,$8,$9)",
        )
        .bind(item_view.id.into_uuid())
        .bind(space_id.into_uuid())
        .bind(member_id.into_uuid())
        .bind(thread_id.into_uuid())
        .bind(inbox_kind_str(kind))
        .bind(item_view.available_at)
        .bind(i64::try_from(message_seq).map_err(|_| ApplicationError::Internal)?)
        .bind(ambient.force_at)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn inbox_snapshot(
        &mut self,
        item_id: InboxItemId,
    ) -> Result<InboxItemSnapshot, ApplicationError> {
        let row = sqlx::query(
            "SELECT kind,strength,thread_id,task_id,message_id,available_at \
             FROM inbox_items WHERE id=$1",
        )
        .bind(item_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let message = match row.get::<Option<Uuid>, _>("message_id") {
            Some(message_id) => {
                let message = sqlx::query(
                    "SELECT id,author_member_id,channel_seq,content_kind,body_markdown, \
                     action_channel_id,action_agent_member_id,created_at FROM messages WHERE id=$1",
                )
                .bind(message_id)
                .fetch_one(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
                Some(wire_message(&message)?)
            }
            None => None,
        };
        Ok(InboxItemSnapshot {
            item_id,
            source_kind: wire_inbox_kind(row.get("kind"))?,
            strength: wire_strength(row.get("strength"))?,
            thread_id: ThreadId::from_uuid(row.get("thread_id")),
            task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
            message,
            available_at: row.get("available_at"),
        })
    }

    pub(super) async fn attention_notice(
        &mut self,
        item_id: InboxItemId,
        location_visible: bool,
    ) -> Result<AttentionNotice, ApplicationError> {
        let row = sqlx::query(
            "SELECT kind,strength,thread_id,task_id,available_at FROM inbox_items WHERE id=$1",
        )
        .bind(item_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let location = if location_visible {
            NoticeLocation::Visible {
                task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
                thread_id: ThreadId::from_uuid(row.get("thread_id")),
            }
        } else {
            NoticeLocation::Restricted
        };
        Ok(AttentionNotice {
            notice_id: NoticeId::from_uuid(item_id.into_uuid()),
            source_kind: wire_inbox_kind(row.get("kind"))?,
            strength: wire_strength(row.get("strength"))?,
            location,
            explicit_human_redirect: false,
            arrived_at: row.get("available_at"),
        })
    }
}
