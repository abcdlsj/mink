use super::*;

use crate::server::application::ports::{InboxActivityEventKind, InboxActivityEventView};

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
            "SELECT i.id,i.space_id,i.member_id,i.kind,i.strength,i.status,i.available_at,i.created_at,\
                    i.retry_count,i.requeue_count,\
                    i.thread_id,i.message_id,t.channel_id,c.slug AS channel_slug,\
                    m.author_member_id AS sender_member_id,sender.display_name AS sender_name,\
                    left(m.body_markdown, 512) AS message_preview \
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
        let mut view = inbox_view_from_row(&row)?;
        view.activity_events = self.activity_events(item_id).await?;
        Ok(view)
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
            "UPDATE inbox_items SET task_id=$2,status=$3,available_at=$4,assigned_run_id=$5, \
             retry_count=$6,handled_at=$7,requeue_count=$8, \
             last_error_code=CASE WHEN $3='assigned' THEN NULL ELSE last_error_code END WHERE id=$1",
        )
        .bind(item.id.into_uuid())
        .bind(item.task_id.map(TaskId::into_uuid))
        .bind(inbox_status_str(item.status))
        .bind(item.available_at)
        .bind(item.assigned_run_id.map(RunId::into_uuid))
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

    pub(super) async fn insert_inbox_item(
        &mut self,
        item: InboxItem,
    ) -> Result<(), ApplicationError> {
        let item = item.snapshot();
        sqlx::query(
            "INSERT INTO inbox_items \
             (id,space_id,member_id,message_id,thread_id,task_id,kind,strength,status, \
              available_at,created_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,'hard','pending',$8,$8)",
        )
        .bind(item.id.into_uuid())
        .bind(item.space_id.into_uuid())
        .bind(item.member_id.into_uuid())
        .bind(item.message_id.map(MessageId::into_uuid))
        .bind(item.thread_id.into_uuid())
        .bind(item.task_id.map(TaskId::into_uuid))
        .bind(inbox_kind_str(item.kind))
        .bind(item.available_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn inbox_for_member(
        &mut self,
        member_id: MemberId,
        scope: InboxScope,
    ) -> Result<Vec<InboxItemView>, ApplicationError> {
        let statuses: &[&str] = match scope {
            InboxScope::Queue => &["pending", "assigned", "deferred"],
            InboxScope::Dead => &["dead"],
        };
        let rows = sqlx::query(
            "SELECT i.id,i.space_id,i.member_id,i.kind,i.strength,i.status,i.available_at,i.created_at,\
                    i.retry_count,i.requeue_count,\
                    i.thread_id,i.message_id,t.channel_id,c.slug AS channel_slug,\
                    m.author_member_id AS sender_member_id,sender.display_name AS sender_name,\
                    left(m.body_markdown, 512) AS message_preview \
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
        let mut views = rows
            .iter()
            .map(inbox_view_from_row)
            .collect::<Result<Vec<_>, _>>()?;
        let item_ids = views
            .iter()
            .map(|item| item.id.into_uuid())
            .collect::<Vec<_>>();
        let events = self.activity_events_for_items(&item_ids).await?;
        for view in &mut views {
            view.activity_events = events
                .get(&view.id.into_uuid())
                .cloned()
                .unwrap_or_default();
        }
        Ok(views)
    }

    async fn activity_events(
        &mut self,
        item_id: InboxItemId,
    ) -> Result<Vec<InboxActivityEventView>, ApplicationError> {
        let events = self
            .activity_events_for_items(&[item_id.into_uuid()])
            .await?;
        Ok(events
            .get(&item_id.into_uuid())
            .cloned()
            .unwrap_or_default())
    }

    async fn activity_events_for_items(
        &mut self,
        item_ids: &[Uuid],
    ) -> Result<std::collections::BTreeMap<Uuid, Vec<InboxActivityEventView>>, ApplicationError>
    {
        if item_ids.is_empty() {
            return Ok(std::collections::BTreeMap::new());
        }
        let rows = sqlx::query(
            "SELECT inbox_item_id,channel_seq,kind,message_id,member_id \
             FROM inbox_activity_events WHERE inbox_item_id=ANY($1) ORDER BY inbox_item_id,channel_seq",
        )
        .bind(item_ids)
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let mut events = std::collections::BTreeMap::<Uuid, Vec<InboxActivityEventView>>::new();
        for row in rows {
            let kind = match row.get::<&str, _>("kind") {
                "message" => InboxActivityEventKind::Message,
                "member_joined" => InboxActivityEventKind::MemberJoined,
                "member_left" => InboxActivityEventKind::MemberLeft,
                _ => return Err(ApplicationError::Internal),
            };
            events
                .entry(row.get("inbox_item_id"))
                .or_default()
                .push(InboxActivityEventView {
                    sequence: u64::try_from(row.get::<i64, _>("channel_seq"))
                        .map_err(|_| ApplicationError::Internal)?,
                    kind,
                    message_id: row
                        .get::<Option<Uuid>, _>("message_id")
                        .map(MessageId::from_uuid),
                    member_id: row
                        .get::<Option<Uuid>, _>("member_id")
                        .map(MemberId::from_uuid),
                });
        }
        Ok(events)
    }

    /// Reads one Item regardless of status, unlike `inbox_for_member`, which projects only the queue.
    /// A governor acting on a retired Item needs to see the result of that action.
    pub(super) async fn route_ambient_activity(
        &mut self,
        input: AmbientActivityInput,
    ) -> Result<(), ApplicationError> {
        let AmbientActivityInput {
            space_id,
            member_id,
            channel_id,
            thread_id,
            kind,
            event,
        } = input;
        let open = if kind == InboxItemKind::ChannelActivity {
            sqlx::query(
                "SELECT * FROM inbox_items \
                 WHERE member_id=$1 AND ambient_channel_id=$2 AND kind='channel_activity' \
                   AND strength='ambient' AND status='pending' AND retry_count=0 \
                 FOR UPDATE",
            )
            .bind(member_id.into_uuid())
            .bind(channel_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
        } else {
            sqlx::query(
                "SELECT * FROM inbox_items \
                 WHERE member_id=$1 AND thread_id=$2 AND kind='thread_activity' \
                   AND strength='ambient' AND status='pending' AND retry_count=0 \
                 FOR UPDATE",
            )
            .bind(member_id.into_uuid())
            .bind(thread_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
        };
        if let Some(row) = open {
            let mut item = inbox_from_row(&row)?;
            // A Message this aggregate already covers is a replay; the range and count stand.
            if item
                .absorb_ambient_message(event.channel_seq, event.now)
                .is_err()
            {
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
            self.insert_activity_event(item_view.id, &event).await?;
            return Ok(());
        }
        let item = InboxItem::open_ambient(
            InboxItemId::from_uuid(Uuid::now_v7()),
            space_id,
            member_id,
            thread_id,
            kind,
            event.channel_seq,
            event.now,
        )?;
        let item_view = item.view();
        let ambient = item_view.ambient.ok_or(ApplicationError::Internal)?;
        sqlx::query(
            "INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,task_id,kind,\
             strength,status,available_at,first_message_seq,last_message_seq,aggregated_count,\
             force_at,ambient_channel_id,created_at) \
             VALUES($1,$2,$3,NULL,$4,NULL,$5,'ambient','pending',$6,$7,$7,1,$8,$9,$10)",
        )
        .bind(item_view.id.into_uuid())
        .bind(space_id.into_uuid())
        .bind(member_id.into_uuid())
        .bind(thread_id.into_uuid())
        .bind(inbox_kind_str(kind))
        .bind(item_view.available_at)
        .bind(i64::try_from(event.channel_seq).map_err(|_| ApplicationError::Internal)?)
        .bind(ambient.force_at)
        .bind((kind == InboxItemKind::ChannelActivity).then_some(channel_id.into_uuid()))
        .bind(event.now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        self.insert_activity_event(item_view.id, &event).await?;
        Ok(())
    }

    async fn insert_activity_event(
        &mut self,
        item_id: InboxItemId,
        event: &AmbientActivityEvent,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO inbox_activity_events(\
                inbox_item_id,channel_id,channel_seq,kind,message_id,member_id,created_at\
             ) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (inbox_item_id,channel_seq) DO NOTHING",
        )
        .bind(item_id.into_uuid())
        .bind(event.channel_id.into_uuid())
        .bind(i64::try_from(event.channel_seq).map_err(|_| ApplicationError::Internal)?)
        .bind(activity_event_kind_str(event.kind))
        .bind(event.message_id.map(MessageId::into_uuid))
        .bind(event.member_id.map(MemberId::into_uuid))
        .bind(event.now)
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
            "SELECT i.kind,i.strength,i.thread_id,i.task_id,i.message_id,i.available_at,t.channel_id \
             FROM inbox_items i \
             JOIN messages t ON t.id=i.thread_id AND t.placement='root' \
             WHERE i.id=$1",
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
        let activity_events = if row.get::<&str, _>("strength") == "ambient" {
            sqlx::query(
                "SELECT channel_seq,kind,message_id,member_id \
                 FROM inbox_activity_events WHERE inbox_item_id=$1 ORDER BY channel_seq",
            )
            .bind(item_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
            .into_iter()
            .map(|event| {
                Ok(ActivityEventSnapshot {
                    sequence: u64::try_from(event.get::<i64, _>("channel_seq"))
                        .map_err(|_| ApplicationError::Internal)?,
                    kind: wire_activity_event_kind(event.get("kind"))?,
                    message_id: event
                        .get::<Option<Uuid>, _>("message_id")
                        .map(MessageId::from_uuid),
                    member_id: event
                        .get::<Option<Uuid>, _>("member_id")
                        .map(MemberId::from_uuid),
                })
            })
            .collect::<Result<Vec<_>, ApplicationError>>()?
        } else {
            Vec::new()
        };
        Ok(InboxItemSnapshot {
            item_id,
            source_kind: wire_inbox_kind(row.get("kind"))?,
            strength: wire_strength(row.get("strength"))?,
            channel_id: ChannelId::from_uuid(row.get("channel_id")),
            thread_id: ThreadId::from_uuid(row.get("thread_id")),
            task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
            message,
            activity_events,
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
