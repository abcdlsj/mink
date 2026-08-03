use super::*;

impl PostgresTransaction {
    pub(super) async fn channel_member_visible(
        &mut self,
        channel_id: ChannelId,
        member_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM channel_members WHERE channel_id=$1 AND member_id=$2)",
        )
        .bind(channel_id.into_uuid())
        .bind(member_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }
    pub(super) async fn message_sequence_in_channel(
        &mut self,
        message_id: MessageId,
        channel_id: ChannelId,
    ) -> Result<Option<u64>, ApplicationError> {
        let value: Option<i64> =
            sqlx::query_scalar("SELECT channel_seq FROM messages WHERE id=$1 AND channel_id=$2")
                .bind(message_id.into_uuid())
                .bind(channel_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        value
            .map(|v| u64::try_from(v).map_err(|_| ApplicationError::Internal))
            .transpose()
    }
    pub(super) async fn channel_snapshot(
        &mut self,
        channel_id: ChannelId,
    ) -> Result<u64, ApplicationError> {
        let value: i64 = sqlx::query_scalar("SELECT next_seq-1 FROM channels WHERE id=$1")
            .bind(channel_id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        u64::try_from(value).map_err(|_| ApplicationError::Internal)
    }

    pub(super) async fn join_channel(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        now: OffsetDateTime,
    ) -> Result<bool, ApplicationError> {
        let space_id: Uuid = sqlx::query_scalar("SELECT space_id FROM channels WHERE id=$1")
            .bind(channel_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
            .ok_or(ApplicationError::NotFound)?;
        let inserted: Option<Uuid> = sqlx::query_scalar(
            "INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) \
             VALUES($1,$2,$3,$4,0) ON CONFLICT (channel_id,member_id) DO NOTHING \
             RETURNING member_id",
        )
        .bind(channel_id.into_uuid())
        .bind(space_id)
        .bind(actor.into_uuid())
        .bind(now)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if inserted.is_some() {
            let display_name: String =
                sqlx::query_scalar("SELECT display_name FROM members WHERE id=$1")
                    .bind(actor.into_uuid())
                    .fetch_one(&mut *self.connection)
                    .await
                    .map_err(map_sqlx)?;
            self.record_channel_member_joined(channel_id, actor, actor, &display_name, now)
                .await?;
        }
        Ok(inserted.is_some())
    }

    pub(super) async fn add_channel_agents(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        agent_ids: Vec<MemberId>,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<Vec<MemberId>, ApplicationError> {
        let channel = sqlx::query(
            "SELECT c.space_id,c.kind,m.access_level FROM channels c \
             JOIN members m ON m.id=$2 AND m.space_id=c.space_id \
             JOIN channel_members cm ON cm.channel_id=c.id AND cm.member_id=m.id \
             WHERE c.id=$1 FOR UPDATE OF c",
        )
        .bind(channel_id.into_uuid())
        .bind(actor.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .ok_or(ApplicationError::NotFound)?;
        if !matches!(channel.get::<&str, _>("access_level"), "owner" | "admin") {
            return Err(ApplicationError::PermissionDenied);
        }
        if channel.get::<&str, _>("kind") == "direct" {
            return Err(ApplicationError::Conflict);
        }
        let space_id: Uuid = channel.get("space_id");
        let ids: Vec<Uuid> = agent_ids.iter().map(|id| id.into_uuid()).collect();
        let valid: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM members m JOIN agents a ON a.member_id=m.id \
             WHERE m.space_id=$1 AND m.kind='agent' AND a.lifecycle<>'retired' AND m.id=ANY($2)",
        )
        .bind(space_id)
        .bind(&ids)
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;

        if valid != ids.len() as i64 {
            return Err(ApplicationError::Conflict);
        }
        self.lock_idempotency(actor, "channel.members.add", idempotency_key)
            .await?;
        let replayed: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM idempotency_records WHERE actor_member_id=$1 \
             AND action='channel.members.add' AND idempotency_key=$2 AND resource_id=$3)",
        )
        .bind(actor.into_uuid())
        .bind(idempotency_key.into_uuid())
        .bind(channel_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if replayed {
            return Ok(Vec::new());
        }
        let inserted: Vec<Uuid> = sqlx::query_scalar(
            "INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) \
             SELECT $1,$2,requested.member_id,$4,0 FROM UNNEST($3::uuid[]) AS requested(member_id) \
             ON CONFLICT DO NOTHING RETURNING member_id",
        )
        .bind(channel_id.into_uuid())
        .bind(space_id)
        .bind(&ids)
        .bind(now)
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let mut hasher = Sha256::new();
        hasher.update(channel_id.into_uuid().as_bytes());
        for id in &ids {
            hasher.update(id.as_bytes());
        }
        sqlx::query("INSERT INTO idempotency_records(actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) VALUES($1,'channel.members.add',$2,'ok',$3,$4,$5)")
            .bind(actor.into_uuid()).bind(idempotency_key.into_uuid()).bind(channel_id.into_uuid()).bind(hasher.finalize().as_slice()).bind(now)
            .execute(&mut *self.connection).await.map_err(map_sqlx)?;
        if !inserted.is_empty() {
            sqlx::query("INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,metadata_json,created_at) VALUES($1,$2,$3,'channel.members_added','channel',$4,$5,$6)")
                .bind(Uuid::now_v7()).bind(space_id).bind(actor.into_uuid()).bind(channel_id.into_uuid()).bind(serde_json::json!({"added_count": inserted.len()})).bind(now)
                .execute(&mut *self.connection).await.map_err(map_sqlx)?;
            for member_id in &inserted {
                let display_name: String =
                    sqlx::query_scalar("SELECT display_name FROM members WHERE id=$1")
                        .bind(member_id)
                        .fetch_one(&mut *self.connection)
                        .await
                        .map_err(map_sqlx)?;
                self.record_channel_member_joined(
                    channel_id,
                    actor,
                    MemberId::from_uuid(*member_id),
                    &display_name,
                    now,
                )
                .await?;
            }
        }
        Ok(inserted.into_iter().map(MemberId::from_uuid).collect())
    }

    pub(super) async fn record_channel_member_joined(
        &mut self,
        channel_id: ChannelId,
        actor: MemberId,
        member_id: MemberId,
        display_name: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let space_id: Uuid = sqlx::query_scalar("SELECT space_id FROM channels WHERE id=$1")
            .bind(channel_id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        self.insert_system_notice(
            channel_id,
            actor,
            format!("{display_name} joined the channel"),
            now,
        )
        .await?;
        sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,'member.changed',$3,$4)")
            .bind(Uuid::now_v7())
            .bind(space_id)
            .bind(serde_json::json!({"resource_id": member_id.into_uuid(), "channel_id": channel_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    async fn insert_system_notice(
        &mut self,
        channel_id: ChannelId,
        author: MemberId,
        body: String,
        now: OffsetDateTime,
    ) -> Result<MessageId, ApplicationError> {
        let message_id = MessageId::from_uuid(Uuid::now_v7());
        let sequence: i64 = sqlx::query_scalar(
            "UPDATE channels SET next_seq=next_seq+1 WHERE id=$1 RETURNING next_seq-1",
        )
        .bind(channel_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,reply_to_message_id,author_member_id,body_markdown,created_at) \
             SELECT $1,$2,$3,$1,$4,'root','system_notice',NULL,$5,$6,$7 FROM channels WHERE id=$3",
        )
        .bind(message_id.into_uuid())
        .bind(sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM channels WHERE id=$1").bind(channel_id.into_uuid()).fetch_one(&mut *self.connection).await.map_err(map_sqlx)?)
        .bind(channel_id.into_uuid())
        .bind(sequence)
        .bind(author.into_uuid())
        .bind(&body)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,'message.created',$3,$4)")
            .bind(Uuid::now_v7())
            .bind(sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM channels WHERE id=$1").bind(channel_id.into_uuid()).fetch_one(&mut *self.connection).await.map_err(map_sqlx)?)
            .bind(serde_json::json!({"channel_id": channel_id.into_uuid(), "message_id": message_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(message_id)
    }

    pub(super) async fn remove_channel_agent(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        agent_id: MemberId,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let channel = sqlx::query(
            "SELECT c.space_id,c.kind,m.access_level FROM channels c \
             JOIN members m ON m.id=$2 AND m.space_id=c.space_id \
             JOIN channel_members cm ON cm.channel_id=c.id AND cm.member_id=m.id \
             WHERE c.id=$1 FOR UPDATE OF c",
        )
        .bind(channel_id.into_uuid())
        .bind(actor.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .ok_or(ApplicationError::NotFound)?;
        if !matches!(channel.get::<&str, _>("access_level"), "owner" | "admin")
            || channel.get::<&str, _>("kind") == "direct"
        {
            return Err(ApplicationError::PermissionDenied);
        }
        let space_id: Uuid = channel.get("space_id");
        let display_name: String = sqlx::query_scalar(
            "SELECT m.display_name FROM members m JOIN agents a ON a.member_id=m.id \
             WHERE m.id=$1 AND m.space_id=$2 AND m.kind='agent' AND a.lifecycle<>'retired'",
        )
        .bind(agent_id.into_uuid())
        .bind(space_id)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .ok_or(ApplicationError::NotFound)?;
        let deleted =
            sqlx::query("DELETE FROM channel_members WHERE channel_id=$1 AND member_id=$2")
                .bind(channel_id.into_uuid())
                .bind(agent_id.into_uuid())
                .execute(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        if deleted.rows_affected() == 0 {
            return Err(ApplicationError::NotFound);
        }
        self.insert_system_notice(
            channel_id,
            actor,
            format!("{display_name} left the channel"),
            now,
        )
        .await?;
        sqlx::query("INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,metadata_json,created_at) VALUES($1,$2,$3,'channel.member_removed','channel',$4,$5,$6)")
            .bind(Uuid::now_v7())
            .bind(space_id)
            .bind(actor.into_uuid())
            .bind(channel_id.into_uuid())
            .bind(serde_json::json!({"member_id": agent_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,'member.changed',$3,$4)")
            .bind(Uuid::now_v7())
            .bind(space_id)
            .bind(serde_json::json!({"resource_id":agent_id.into_uuid(), "channel_id":channel_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn leave_channel(
        &mut self,
        agent_id: MemberId,
        channel_id: ChannelId,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        if let Some(resource_id) = self
            .resource_for_idempotency(agent_id, "channel.leave", idempotency_key)
            .await?
        {
            if resource_id != channel_id.into_uuid() {
                return Err(ApplicationError::Conflict);
            }
            return Ok(());
        }
        let channel = sqlx::query("SELECT space_id,kind FROM channels WHERE id=$1 FOR UPDATE")
            .bind(channel_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
            .ok_or(ApplicationError::NotFound)?;
        if channel.get::<&str, _>("kind") == "direct" {
            return Err(ApplicationError::Conflict);
        }
        let space_id: Uuid = channel.get("space_id");
        let display_name: String = sqlx::query_scalar(
            "SELECT m.display_name FROM members m JOIN agents a ON a.member_id=m.id \
             JOIN channel_members cm ON cm.channel_id=$2 AND cm.member_id=m.id \
             WHERE m.id=$1 AND m.space_id=$3 AND m.kind='agent' AND a.lifecycle<>'retired'",
        )
        .bind(agent_id.into_uuid())
        .bind(channel_id.into_uuid())
        .bind(space_id)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .ok_or(ApplicationError::NotFound)?;
        let deleted =
            sqlx::query("DELETE FROM channel_members WHERE channel_id=$1 AND member_id=$2")
                .bind(channel_id.into_uuid())
                .bind(agent_id.into_uuid())
                .execute(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        if deleted.rows_affected() == 0 {
            return Err(ApplicationError::NotFound);
        }
        self.record_resource_idempotency(
            agent_id,
            "channel.leave",
            idempotency_key,
            channel_id.into_uuid(),
        )
        .await?;
        self.insert_system_notice(
            channel_id,
            agent_id,
            format!("{display_name} left the channel"),
            now,
        )
        .await?;
        sqlx::query("INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,metadata_json,created_at) VALUES($1,$2,$3,'channel.member_left','channel',$4,$5,$6)")
            .bind(Uuid::now_v7())
            .bind(space_id)
            .bind(agent_id.into_uuid())
            .bind(channel_id.into_uuid())
            .bind(serde_json::json!({"member_id": agent_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,'member.changed',$3,$4)")
            .bind(Uuid::now_v7())
            .bind(space_id)
            .bind(serde_json::json!({"resource_id":agent_id.into_uuid(), "channel_id":channel_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }
    pub(super) async fn channel_action_audience(
        &mut self,
        focus_thread_id: ThreadId,
        space_id: SpaceId,
        private: bool,
    ) -> Result<BTreeSet<MemberId>, ApplicationError> {
        let ids = if private {
            sqlx::query_scalar::<_, Uuid>(
                "SELECT member_id FROM channel_members \
                 WHERE channel_id=(SELECT channel_id FROM messages WHERE id=$1 AND placement='root')",
            )
            .bind(focus_thread_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
        } else {
            sqlx::query_scalar::<_, Uuid>(
                "SELECT id FROM members WHERE space_id=$1 AND retired_at IS NULL",
            )
            .bind(space_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
        };
        Ok(ids.into_iter().map(MemberId::from_uuid).collect())
    }

    pub(super) async fn channel_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<ChannelId>, ApplicationError> {
        Ok(sqlx::query_scalar::<_, Uuid>(
            "SELECT channel_id FROM messages WHERE id=$1 AND placement='root'",
        )
        .bind(thread_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .map(ChannelId::from_uuid))
    }
    pub(super) async fn direct_messages_for_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Vec<DirectMessageView>, ApplicationError> {
        let rows = sqlx::query(
            "SELECT c.id AS channel_id,c.created_at, \
                    other.id,other.kind,other.display_name,other.access_level \
             FROM channel_members mine \
             JOIN channels c ON c.id=mine.channel_id \
             JOIN channel_members theirs \
               ON theirs.channel_id=c.id AND theirs.member_id<>mine.member_id \
             JOIN members other ON other.id=theirs.member_id \
             WHERE mine.member_id=$1 AND c.space_id=$2 AND c.kind='direct' \
             ORDER BY COALESCE( \
               (SELECT max(created_at) FROM messages WHERE channel_id=c.id),c.created_at \
             ) DESC,c.id DESC",
        )
        .bind(member_id.into_uuid())
        .bind(space_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let mut views = Vec::with_capacity(rows.len());
        for row in &rows {
            let permissions = self.member_permissions(row.get("id")).await?;
            views.push(direct_message_from_row(row, space_id, permissions)?);
        }
        Ok(views)
    }

    pub(super) async fn message(&mut self, id: MessageId) -> Result<Message, ApplicationError> {
        let row = sqlx::query("SELECT * FROM messages WHERE id=$1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        message_from_row(&row)
    }

    pub(super) async fn set_thread_subscription(
        &mut self,
        thread_id: ThreadId,
        member_id: MemberId,
        following: bool,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        if following {
            sqlx::query(
                "INSERT INTO thread_subscriptions(thread_id,space_id,member_id,created_at) \
                 SELECT $1,t.space_id,$2,$3 FROM messages t \
                 WHERE t.id=$1 AND t.placement='root' \
                 ON CONFLICT (thread_id,member_id) DO NOTHING",
            )
            .bind(thread_id.into_uuid())
            .bind(member_id.into_uuid())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        } else {
            sqlx::query("DELETE FROM thread_subscriptions WHERE thread_id=$1 AND member_id=$2")
                .bind(thread_id.into_uuid())
                .bind(member_id.into_uuid())
                .execute(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        }
        Ok(())
    }

    pub(super) async fn direct_message_between(
        &mut self,
        space_id: SpaceId,
        first: MemberId,
        second: MemberId,
    ) -> Result<Option<DirectMessageView>, ApplicationError> {
        let row = sqlx::query(
            "SELECT c.id AS channel_id,c.created_at, \
                    other.id,other.kind,other.display_name,other.access_level \
             FROM channels c \
             JOIN channel_members mine ON mine.channel_id=c.id AND mine.member_id=$2 \
             JOIN channel_members theirs ON theirs.channel_id=c.id AND theirs.member_id=$3 \
             JOIN members other ON other.id=theirs.member_id \
             WHERE c.space_id=$1 AND c.kind='direct'",
        )
        .bind(space_id.into_uuid())
        .bind(first.into_uuid())
        .bind(second.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        match row {
            Some(row) => {
                let permissions = self.member_permissions(row.get("id")).await?;
                Ok(Some(direct_message_from_row(&row, space_id, permissions)?))
            }
            None => Ok(None),
        }
    }

    pub(super) async fn publish_message(
        &mut self,
        draft: MessageDraft,
    ) -> Result<PublishedMessage, ApplicationError> {
        let channel = sqlx::query(
            "SELECT space_id,kind,next_seq-1 AS snapshot FROM channels WHERE id=$1 FOR UPDATE",
        )
        .bind(draft.channel_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let space_id: Uuid = channel.get("space_id");
        let channel_kind: String = channel.get("kind");
        let snapshot = u64::try_from(channel.get::<i64, _>("snapshot"))
            .map_err(|_| ApplicationError::Internal)?;
        if draft
            .expected_snapshot
            .is_some_and(|expected| expected != snapshot)
        {
            return Err(ApplicationError::ContextChanged);
        }
        let thread_id = draft
            .thread_id
            .unwrap_or_else(|| ThreadId::from_uuid(draft.message_id.into_uuid()));
        let channel_sequence: i64 = sqlx::query_scalar(
            "UPDATE channels SET next_seq=next_seq+1 WHERE id=$1 RETURNING next_seq-1",
        )
        .bind(draft.channel_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,\
             content_kind,reply_to_message_id,author_member_id,body_markdown,mention_all,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,'text',$7,$8,$9,$10,$11)",
        )
        .bind(draft.message_id.into_uuid())
        .bind(space_id)
        .bind(draft.channel_id.into_uuid())
        .bind(thread_id.into_uuid())
        .bind(channel_sequence)
        .bind(if draft.thread_id.is_some() {
            "reply"
        } else {
            "root"
        })
        .bind(draft.reply_to_message_id.map(MessageId::into_uuid))
        .bind(draft.author_member_id.into_uuid())
        .bind(&draft.body_markdown)
        .bind(draft.mention_all)
        .bind(draft.now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        // Mention targets are persisted as structured relations so projections and consumers do not
        // need to infer recipients from Message Markdown. Ignore IDs that are not Channel members;
        // routing below still validates the Agent subset through the Channel membership query.
        if !draft.mentions.is_empty() {
            let mention_ids: Vec<Uuid> = draft.mentions.iter().map(|id| id.into_uuid()).collect();
            sqlx::query(
                "INSERT INTO message_mentions(message_id,space_id,member_id,created_at) \
                 SELECT $1,$2,cm.member_id,$3 FROM channel_members cm \
                 JOIN members m ON m.id=cm.member_id AND m.retired_at IS NULL \
                 WHERE cm.channel_id=$4 AND cm.member_id=ANY($5::uuid[]) \
                 ON CONFLICT DO NOTHING",
            )
            .bind(draft.message_id.into_uuid())
            .bind(space_id)
            .bind(draft.now)
            .bind(draft.channel_id.into_uuid())
            .bind(&mention_ids)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        if draft.mention_all {
            sqlx::query(
                "INSERT INTO message_mentions(message_id,space_id,member_id,created_at) \
                 SELECT $1,$2,cm.member_id,$3 FROM channel_members cm \
                 JOIN members m ON m.id=cm.member_id AND m.retired_at IS NULL \
                 WHERE cm.channel_id=$4 AND cm.member_id<>$5 ON CONFLICT DO NOTHING",
            )
            .bind(draft.message_id.into_uuid())
            .bind(space_id)
            .bind(draft.now)
            .bind(draft.channel_id.into_uuid())
            .bind(draft.author_member_id.into_uuid())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        for (position, attachment_id) in draft.attachment_ids.into_iter().enumerate() {
            sqlx::query(
                "INSERT INTO message_attachments(message_id,attachment_id,space_id,position) \
                 VALUES($1,$2,$3,$4)",
            )
            .bind(draft.message_id.into_uuid())
            .bind(attachment_id.into_uuid())
            .bind(space_id)
            .bind(i32::try_from(position).map_err(|_| ApplicationError::Conflict)?)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        if let Some((run_id, item_id)) = draft.handled_item {
            let changed = sqlx::query(
                "UPDATE run_items SET disposition='handled' WHERE run_id=$1 AND inbox_item_id=$2 \
                 AND (disposition IS NULL OR disposition='handled')",
            )
            .bind(run_id.into_uuid())
            .bind(item_id.into_uuid())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
            if changed.rows_affected() != 1 {
                return Err(ApplicationError::ContextChanged);
            }
        }
        let task_id = sqlx::query_scalar::<_, Uuid>(
            "SELECT id FROM tasks WHERE status NOT IN ('done','closed') AND \
             (source_thread_id=$1 OR EXISTS(SELECT 1 FROM task_threads \
              WHERE task_id=tasks.id AND thread_id=$1)) LIMIT 1",
        )
        .bind(thread_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let reply_author = if let Some(reply_to) = draft.reply_to_message_id {
            sqlx::query_scalar::<_, Uuid>(
                "SELECT author_member_id FROM messages WHERE id=$1 AND thread_id=$2",
            )
            .bind(reply_to.into_uuid())
            .bind(thread_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
        } else {
            None
        };
        // Attention routes to every non-retired Member except the sender. Human recipients receive
        // hard Items (DM, mention, reply, Task activity) and subscribed-Thread activity, but never
        // plain Channel activity: ordinary Channel Messages are already visible where they live.
        let recipients = sqlx::query(
            "SELECT members.id,members.kind FROM channel_members JOIN members \
             ON members.id=channel_members.member_id WHERE channel_members.channel_id=$1 \
             AND members.retired_at IS NULL AND members.id<>$2",
        )
        .bind(draft.channel_id.into_uuid())
        .bind(draft.author_member_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let recipients = recipients
            .into_iter()
            .map(|row| {
                (
                    MemberId::from_uuid(row.get::<Uuid, _>("id")),
                    row.get::<String, _>("kind") == "agent",
                )
            })
            .collect::<Vec<_>>();
        // An explicit Thread subscription raises ordinary activity above channel_activity. Read only
        // for a reply: a Root Message creates its Thread, which nobody can have subscribed to yet.
        let subscribers = if draft.thread_id.is_some() {
            sqlx::query_scalar::<_, Uuid>(
                "SELECT member_id FROM thread_subscriptions WHERE thread_id=$1",
            )
            .bind(thread_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
            .into_iter()
            .collect::<BTreeSet<_>>()
        } else {
            BTreeSet::new()
        };
        let mut mentioned = draft.mentions.into_iter().collect::<BTreeSet<_>>();
        if draft.mention_all {
            mentioned.extend(recipients.iter().map(|(member_id, _)| *member_id));
        }
        let message_seq =
            u64::try_from(channel_sequence).map_err(|_| ApplicationError::Internal)?;
        let mut hard_item_ids = Vec::new();
        let mut notified_member_ids = Vec::new();
        for (member_id, is_agent) in recipients {
            // One Message yields one Item per Member at its highest strength. Ambient activity merges
            // into that Member's open aggregate for the Thread instead of adding a row per Message.
            let kind = if task_id.is_some() {
                InboxItemKind::TaskActivity
            } else if channel_kind == "direct" {
                InboxItemKind::Direct
            } else if mentioned.contains(&member_id) {
                InboxItemKind::Mention
            } else if reply_author == Some(member_id.into_uuid()) {
                InboxItemKind::Reply
            } else {
                let subscribed = subscribers.contains(&member_id.into_uuid());
                if !is_agent && !subscribed {
                    continue;
                }
                let kind = if subscribed {
                    InboxItemKind::ThreadActivity
                } else {
                    InboxItemKind::ChannelActivity
                };
                self.route_ambient_activity(
                    SpaceId::from_uuid(space_id),
                    member_id,
                    thread_id,
                    kind,
                    message_seq,
                    draft.now,
                )
                .await?;
                notified_member_ids.push(member_id);
                continue;
            };
            let item_id = InboxItemId::from_uuid(Uuid::now_v7());
            let item = InboxItem::open_hard(
                item_id,
                SpaceId::from_uuid(space_id),
                member_id,
                Some(draft.message_id),
                thread_id,
                task_id.map(TaskId::from_uuid),
                kind,
                draft.now,
            )?;
            let item = item.view();
            sqlx::query(
                "INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,task_id,kind,\
                 strength,status,available_at,created_at) \
                 VALUES($1,$2,$3,$4,$5,$6,$7,'hard','pending',$8,$8)",
            )
            .bind(item.id.into_uuid())
            .bind(space_id)
            .bind(member_id.into_uuid())
            .bind(draft.message_id.into_uuid())
            .bind(thread_id.into_uuid())
            .bind(task_id)
            .bind(inbox_kind_str(item.kind))
            .bind(draft.now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
            // Only Agent Items enter the active-Run routing pass; Human Items have no Run to
            // attach to or notify.
            if is_agent {
                hard_item_ids.push(item_id);
            }
            notified_member_ids.push(member_id);
        }
        Ok(PublishedMessage {
            message_id: draft.message_id,
            hard_item_ids,
            notified_member_ids,
        })
    }

    pub(super) async fn insert_message(
        &mut self,
        message: Message,
    ) -> Result<(), ApplicationError> {
        let location = sqlx::query(
            "SELECT channel_id,space_id FROM messages WHERE id=$1 AND placement='root'",
        )
        .bind(message.thread_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let channel_id: Uuid = location.get("channel_id");
        let space_id: Uuid = location.get("space_id");
        let channel_seq: i64 = sqlx::query_scalar(
            "UPDATE channels SET next_seq=next_seq+1 WHERE id=$1 RETURNING next_seq-1",
        )
        .bind(channel_id)
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let (kind, body, action_channel, action_agent) = match message.content {
            MessageContent::Text(body) => ("text", Some(body), None, None),
            MessageContent::ChannelCreated(id) => {
                ("channel_created", None, Some(id.into_uuid()), None)
            }
            MessageContent::AgentCreated(id) => ("agent_created", None, None, Some(id.into_uuid())),
            MessageContent::SystemNotice(body) => ("system_notice", Some(body), None, None),
        };
        sqlx::query(
            "INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement, \
             content_kind,reply_to_message_id,author_member_id,body_markdown,action_channel_id, \
             action_agent_member_id,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,$10,$11,$12)",
        )
        .bind(message.id.into_uuid())
        .bind(space_id)
        .bind(channel_id)
        .bind(message.thread_id.into_uuid())
        .bind(channel_seq)
        .bind(placement_str(message.placement))
        .bind(kind)
        .bind(message.author_member_id.into_uuid())
        .bind(body)
        .bind(action_channel)
        .bind(action_agent)
        .bind(message.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn insert_channel(
        &mut self,
        channel: Channel,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO channels (id,space_id,kind,slug,topic,next_seq,created_at) \
             VALUES ($1,$2,$3,$4,$5,1,$6)",
        )
        .bind(channel.id.into_uuid())
        .bind(channel.space_id.into_uuid())
        .bind(channel_kind_str(channel.kind))
        .bind(&channel.slug)
        .bind(&channel.topic)
        .bind(channel.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        for member in channel.audience {
            sqlx::query(
                "INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) \
                 VALUES ($1,$2,$3,now(),0)",
            )
            .bind(channel.id.into_uuid())
            .bind(channel.space_id.into_uuid())
            .bind(member.into_uuid())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        Ok(())
    }

    pub(super) async fn root_message(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Message, ApplicationError> {
        let row = sqlx::query(
            "SELECT id, thread_id, author_member_id, placement, content_kind, body_markdown, \
                    action_channel_id, action_agent_member_id, created_at, edited_at, deleted_at \
             FROM messages WHERE thread_id = $1 AND placement = 'root' FOR UPDATE",
        )
        .bind(thread_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        message_from_row(&row)
    }

    pub(super) async fn can_read_thread(
        &mut self,
        actor: MemberId,
        thread_id: ThreadId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM messages \
             JOIN channel_members ON channel_members.channel_id = messages.channel_id \
             WHERE messages.id = $1 AND messages.placement = 'root' \
               AND channel_members.member_id = $2)",
        )
        .bind(thread_id.into_uuid())
        .bind(actor.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    pub(super) async fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError> {
        let row = sqlx::query(
            "SELECT id, space_id, channel_id FROM messages \
             WHERE id = $1 AND placement = 'root' FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let audience = self.thread_audience(id).await?;
        Ok(Thread {
            id: ThreadId::from_uuid(row.get("id")),
            space_id: SpaceId::from_uuid(row.get("space_id")),
            channel_id: ChannelId::from_uuid(row.get("channel_id")),
            audience,
        })
    }

    pub(super) async fn channel(&mut self, id: ChannelId) -> Result<Channel, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,kind,slug,topic,archived_at,created_at FROM channels \
             WHERE id=$1 FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let members = sqlx::query_scalar::<_, Uuid>(
            "SELECT member_id FROM channel_members WHERE channel_id=$1 ORDER BY member_id",
        )
        .bind(id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(Channel {
            id,
            space_id: SpaceId::from_uuid(row.get("space_id")),
            audience: members.into_iter().map(MemberId::from_uuid).collect(),
            kind: channel_kind_from_str(row.get("kind"))?,
            slug: row.get("slug"),
            topic: row.get("topic"),
            archived_at: row.get("archived_at"),
            created_at: row.get("created_at"),
        })
    }

    pub(super) async fn thread_message_sequence(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<u64, ApplicationError> {
        let sequence = sqlx::query_scalar::<_, i64>(
            "SELECT channels.next_seq-1 FROM messages \
             JOIN channels ON channels.id=messages.channel_id \
             WHERE messages.id=$1 AND messages.placement='root' FOR UPDATE OF channels",
        )
        .bind(thread_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        u64::try_from(sequence).map_err(|_| ApplicationError::Internal)
    }

    pub(super) async fn save_message(&mut self, message: Message) -> Result<(), ApplicationError> {
        let MessageContent::Text(body) = message.content else {
            return Err(ApplicationError::Conflict);
        };
        let changed = sqlx::query(
            "UPDATE messages SET body_markdown=$2,edited_at=$3,deleted_at=$4 \
             WHERE id=$1 AND content_kind='text'",
        )
        .bind(message.id.into_uuid())
        .bind(body)
        .bind(message.edited_at)
        .bind(message.deleted_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }

    pub(super) async fn save_message_mentions(
        &mut self,
        message_id: MessageId,
        mentions: Vec<MemberId>,
        mention_all: bool,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let location =
            sqlx::query("SELECT channel_id,space_id,author_member_id FROM messages WHERE id=$1")
                .bind(message_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?
                .ok_or(ApplicationError::NotFound)?;
        let channel_id: Uuid = location.get("channel_id");
        let space_id: Uuid = location.get("space_id");
        let author_id: Uuid = location.get("author_member_id");
        sqlx::query("DELETE FROM message_mentions WHERE message_id=$1")
            .bind(message_id.into_uuid())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        if !mentions.is_empty() {
            let ids: Vec<Uuid> = mentions.iter().map(|id| id.into_uuid()).collect();
            sqlx::query(
                "INSERT INTO message_mentions(message_id,space_id,member_id,created_at) \
                 SELECT $1,$2,cm.member_id,$5 FROM channel_members cm \
                 JOIN members m ON m.id=cm.member_id AND m.retired_at IS NULL \
                 WHERE cm.channel_id=$3 AND cm.member_id=ANY($4::uuid[]) ON CONFLICT DO NOTHING",
            )
            .bind(message_id.into_uuid())
            .bind(space_id)
            .bind(channel_id)
            .bind(&ids)
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        if mention_all {
            sqlx::query(
                "INSERT INTO message_mentions(message_id,space_id,member_id,created_at) \
                 SELECT $1,$2,cm.member_id,$5 FROM channel_members cm \
                 JOIN members m ON m.id=cm.member_id AND m.retired_at IS NULL \
                 WHERE cm.channel_id=$3 AND cm.member_id<>$4 ON CONFLICT DO NOTHING",
            )
            .bind(message_id.into_uuid())
            .bind(space_id)
            .bind(channel_id)
            .bind(author_id)
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        sqlx::query("UPDATE messages SET mention_all=$2 WHERE id=$1")
            .bind(message_id.into_uuid())
            .bind(mention_all)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn save_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
        let changed = sqlx::query("UPDATE channels SET topic=$2,archived_at=$3 WHERE id=$1")
            .bind(channel.id.into_uuid())
            .bind(&channel.topic)
            .bind(channel.archived_at)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        if changed.rows_affected() != 1 {
            return Err(ApplicationError::NotFound);
        }
        for member in channel.audience {
            sqlx::query(
                "INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) \
                 VALUES ($1,$2,$3,now(),0) ON CONFLICT (channel_id,member_id) DO NOTHING",
            )
            .bind(channel.id.into_uuid())
            .bind(channel.space_id.into_uuid())
            .bind(member.into_uuid())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        Ok(())
    }

    pub(super) async fn thread_audience(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<BTreeSet<MemberId>, ApplicationError> {
        sqlx::query_scalar::<_, Uuid>(
            "SELECT channel_members.member_id FROM messages JOIN channel_members \
             ON channel_members.channel_id=messages.channel_id \
             WHERE messages.id=$1 AND messages.placement='root' ORDER BY member_id",
        )
        .bind(thread_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map(|ids| ids.into_iter().map(MemberId::from_uuid).collect())
        .map_err(map_sqlx)
    }
}

#[async_trait]
impl CollaborationTransaction for PostgresTransaction {
    async fn channel_access(
        &mut self,
        user_id: uuid::Uuid,
        channel_id: ChannelId,
    ) -> Result<Option<MemberId>, ApplicationError> {
        self.channel_access(user_id, channel_id).await
    }
    async fn direct_messages_for_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Vec<DirectMessageView>, ApplicationError> {
        self.direct_messages_for_member(member_id, space_id).await
    }
    async fn direct_message_between(
        &mut self,
        space_id: SpaceId,
        first: MemberId,
        second: MemberId,
    ) -> Result<Option<DirectMessageView>, ApplicationError> {
        self.direct_message_between(space_id, first, second).await
    }
    async fn space_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Option<SpaceMemberView>, ApplicationError> {
        self.space_member(member_id, space_id).await
    }
    async fn inbox_for_member(
        &mut self,
        member_id: MemberId,
        scope: InboxScope,
    ) -> Result<Vec<InboxItemView>, ApplicationError> {
        self.inbox_for_member(member_id, scope).await
    }
    async fn inbox_item_view(
        &mut self,
        item_id: InboxItemId,
    ) -> Result<InboxItemView, ApplicationError> {
        self.inbox_item_view(item_id).await
    }
    async fn save_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
        self.save_channel(channel).await
    }
    async fn set_thread_subscription(
        &mut self,
        thread_id: ThreadId,
        member_id: MemberId,
        following: bool,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.set_thread_subscription(thread_id, member_id, following, now)
            .await
    }
    async fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError> {
        self.thread(id).await
    }
    async fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError> {
        self.root_message(thread_id).await
    }
    async fn message(&mut self, id: MessageId) -> Result<Message, ApplicationError> {
        self.message(id).await
    }
    async fn channel(&mut self, id: ChannelId) -> Result<Channel, ApplicationError> {
        self.channel(id).await
    }
    async fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError> {
        self.inbox_item(id).await
    }
    async fn can_read_thread(
        &mut self,
        actor: MemberId,
        thread_id: ThreadId,
    ) -> Result<bool, ApplicationError> {
        self.can_read_thread(actor, thread_id).await
    }
    async fn thread_message_sequence(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<u64, ApplicationError> {
        self.thread_message_sequence(thread_id).await
    }
    async fn publish_message(
        &mut self,
        draft: MessageDraft,
    ) -> Result<PublishedMessage, ApplicationError> {
        self.publish_message(draft).await
    }
    async fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError> {
        self.insert_message(message).await
    }
    async fn save_message(&mut self, message: Message) -> Result<(), ApplicationError> {
        self.save_message(message).await
    }
    async fn save_message_mentions(
        &mut self,
        message_id: MessageId,
        mentions: Vec<MemberId>,
        mention_all: bool,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.save_message_mentions(message_id, mentions, mention_all, now)
            .await
    }
    async fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
        self.insert_channel(channel).await
    }
    async fn channel_action_audience(
        &mut self,
        focus_thread_id: ThreadId,
        space_id: SpaceId,
        private: bool,
    ) -> Result<BTreeSet<MemberId>, ApplicationError> {
        self.channel_action_audience(focus_thread_id, space_id, private)
            .await
    }
    async fn channel_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<ChannelId>, ApplicationError> {
        self.channel_for_thread(thread_id).await
    }
    async fn join_channel(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        now: OffsetDateTime,
    ) -> Result<bool, ApplicationError> {
        self.join_channel(actor, channel_id, now).await
    }
    async fn add_channel_agents(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        agent_ids: Vec<MemberId>,
        idempotency_key: IdempotencyKey,
        now: time::OffsetDateTime,
    ) -> Result<Vec<MemberId>, ApplicationError> {
        self.add_channel_agents(actor, channel_id, agent_ids, idempotency_key, now)
            .await
    }
    async fn remove_channel_agent(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        agent_id: MemberId,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.remove_channel_agent(actor, channel_id, agent_id, now)
            .await
    }
    async fn leave_channel(
        &mut self,
        agent_id: MemberId,
        channel_id: ChannelId,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.leave_channel(agent_id, channel_id, idempotency_key, now)
            .await
    }
    async fn channel_member_visible(
        &mut self,
        channel_id: ChannelId,
        member_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        self.channel_member_visible(channel_id, member_id).await
    }
    async fn message_sequence_in_channel(
        &mut self,
        message_id: MessageId,
        channel_id: ChannelId,
    ) -> Result<Option<u64>, ApplicationError> {
        self.message_sequence_in_channel(message_id, channel_id)
            .await
    }
    async fn channel_snapshot(&mut self, channel_id: ChannelId) -> Result<u64, ApplicationError> {
        self.channel_snapshot(channel_id).await
    }
    async fn pending_item_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        self.pending_item_for_agent(agent_id).await
    }
}
