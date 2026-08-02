use super::*;

pub(super) fn command_kind(command: &Command) -> &'static str {
    match command {
        Command::AgentProvision(_) => "agent.provision",
        Command::AgentConfigure(_) => "agent.configure",
        Command::AgentSuspend(_) => "agent.suspend",
        Command::AgentRetire(_) => "agent.retire",
        Command::RunStart(_) => "run.start",
        Command::RunTaskBound(_) => "run.task_bound",
        Command::RunAttachItem(_) => "run.attach_item",
        Command::RunNotice(_) => "run.notice",
        Command::RunStop(_) => "run.stop",
        Command::SessionReset(_) => "session.reset",
        Command::SessionClose(_) => "session.close",
    }
}

impl PostgresTransaction {
    pub(super) async fn agent_provision_command_target(
        &mut self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<Option<MemberId>, ApplicationError> {
        let sequence = i64::try_from(sequence).map_err(|_| ApplicationError::Conflict)?;
        let row = sqlx::query(
            "SELECT kind,payload_json #>> '{payload,agent_id}' AS agent_id FROM computer_commands \
             WHERE id=$1 AND computer_id=$2 AND computer_seq=$3",
        )
        .bind(command_id.into_uuid())
        .bind(computer_id.into_uuid())
        .bind(sequence)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .ok_or(ApplicationError::NotFound)?;
        if row.get::<&str, _>("kind") != "agent.provision" {
            return Ok(None);
        }
        let agent_id = Uuid::parse_str(row.get::<&str, _>("agent_id"))
            .map_err(|_| ApplicationError::Internal)?;
        Ok(Some(MemberId::from_uuid(agent_id)))
    }
    pub(super) async fn active_run_for_visible_agent(
        &mut self,
        agent_id: MemberId,
        viewer_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError> {
        let id: Option<Uuid> = sqlx::query_scalar("SELECT r.id FROM agent_runs r JOIN threads t ON t.id=r.focus_thread_id JOIN channel_members cm ON cm.channel_id=t.channel_id AND cm.member_id=$2 WHERE r.agent_id=$1 AND r.status NOT IN ('completed','yielded','failed','canceled') ORDER BY r.created_at DESC LIMIT 1")
            .bind(agent_id.into_uuid()).bind(viewer_id.into_uuid()).fetch_optional(&mut *self.connection).await.map_err(map_sqlx)?;
        Ok(id.map(RunId::from_uuid))
    }
    pub(super) async fn pending_item_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM inbox_items WHERE member_id=$1 AND status='pending')",
        )
        .bind(agent_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }
    pub(super) async fn authorize_run_capability(
        &mut self,
        proof: &RunCapabilityProof,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM agent_runs r JOIN agents a ON a.member_id=r.agent_id \
             WHERE r.id=$1 AND r.agent_id=$2 AND r.space_id=$3 AND r.task_id IS NOT DISTINCT FROM $4 \
               AND r.focus_thread_id=$5 AND r.status='running' AND r.fencing_token_hash=$6 \
               AND r.lease_expires_at>now() AND a.computer_id=$7)",
        )
        .bind(proof.run_id.into_uuid())
        .bind(proof.agent_id.into_uuid())
        .bind(proof.space_id.into_uuid())
        .bind(proof.task_id.map(TaskId::into_uuid))
        .bind(proof.focus_thread_id.into_uuid())
        .bind(&proof.fencing_token_hash)
        .bind(proof.computer_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }
    pub(super) async fn next_claim_candidate(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<ClaimCandidate>, ApplicationError> {
        let row = sqlx::query(
            "SELECT i.id,i.member_id,i.task_id,i.thread_id,i.message_id,t.channel_id FROM inbox_items i \
             JOIN agents a ON a.member_id=i.member_id JOIN threads t ON t.id=i.thread_id \
             WHERE a.computer_id=$1 AND a.lifecycle='active' AND i.status='pending' \
               AND i.available_at<=now() \
               AND NOT EXISTS(SELECT 1 FROM agent_runs r WHERE r.agent_id=i.member_id \
                 AND r.status NOT IN ('completed','yielded','failed','canceled')) \
             ORDER BY (i.strength='hard') DESC,i.available_at,(i.task_id IS NOT NULL) DESC,i.id LIMIT 1",
        )
        .bind(computer_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| ClaimCandidate {
            item_id: InboxItemId::from_uuid(row.get("id")),
            agent_id: MemberId::from_uuid(row.get("member_id")),
            task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
            thread_id: ThreadId::from_uuid(row.get("thread_id")),
            message_id: row
                .get::<Option<Uuid>, _>("message_id")
                .map(MessageId::from_uuid),
            channel_id: ChannelId::from_uuid(row.get("channel_id")),
        }))
    }

    pub(super) async fn record_claim_failure(
        &mut self,
        item_id: InboxItemId,
        message_id: Option<MessageId>,
        channel_id: ChannelId,
        error_code: &str,
    ) -> Result<bool, ApplicationError> {
        let changed = sqlx::query(
            "UPDATE inbox_items SET last_error_code=$2 WHERE id=$1 AND status='pending' \
             AND last_error_code IS DISTINCT FROM $2",
        )
        .bind(item_id.into_uuid())
        .bind(error_code)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .rows_affected()
            == 1;
        if changed && let Some(message_id) = message_id {
            let space_id: Uuid = sqlx::query_scalar("SELECT space_id FROM messages WHERE id=$1")
                .bind(message_id.into_uuid())
                .fetch_one(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
            sqlx::query(
                    "INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) \
                     VALUES($1,$2,'message.updated',$3,now())",
                )
                .bind(Uuid::now_v7())
                .bind(space_id)
                .bind(serde_json::json!({"resource_id":message_id.into_uuid(),"channel_id":channel_id.into_uuid()}))
                .execute(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        }
        Ok(changed)
    }
    pub(super) async fn runs_with_expired_lease(
        &mut self,
        now: OffsetDateTime,
        limit: u32,
    ) -> Result<Vec<RunId>, ApplicationError> {
        sqlx::query_scalar::<_, Uuid>(
            "SELECT id FROM agent_runs \
             WHERE status NOT IN ('completed','yielded','failed','canceled') \
               AND lease_expires_at<=$1 ORDER BY lease_expires_at,id LIMIT $2",
        )
        .bind(now)
        .bind(i64::from(limit))
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)
        .map(|rows| rows.into_iter().map(RunId::from_uuid).collect())
    }

    pub(super) async fn record_completed_run_event(
        &mut self,
        event_id: EventId,
        run_id: RunId,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO run_result_events (event_id,run_id,created_at) VALUES ($1,$2,now())",
        )
        .bind(event_id.into_uuid())
        .bind(run_id.into_uuid())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError> {
        let run = run.snapshot();
        sqlx::query(
            "INSERT INTO agent_runs (id,space_id,agent_id,task_id,focus_thread_id,status, \
             fencing_token_hash,lease_expires_at,outcome_code,error_code,continuation_note,created_at,started_at,finished_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now(),$12,$13) \
             ON CONFLICT (id) DO UPDATE SET task_id=EXCLUDED.task_id,status=EXCLUDED.status, \
             lease_expires_at=EXCLUDED.lease_expires_at,outcome_code=EXCLUDED.outcome_code, \
             error_code=EXCLUDED.error_code, \
             continuation_note=EXCLUDED.continuation_note,started_at=EXCLUDED.started_at,finished_at=EXCLUDED.finished_at",
        )
        .bind(run.id.into_uuid())
        .bind(run.space_id.into_uuid())
        .bind(run.agent_id.into_uuid())
        .bind(run.task_id.map(TaskId::into_uuid))
        .bind(run.focus_thread_id.into_uuid())
        .bind(run_status_str(run.status))
        .bind(&run.fencing_token_hash)
        .bind(run.lease_expires_at)
        .bind(run.outcome.map(run_outcome_str))
        .bind(run.error_code.map(run_error_code_str))
        .bind(&run.continuation_note)
        .bind(run.started_at)
        .bind(run.finished_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        for item in &run.items {
            sqlx::query(
                "INSERT INTO run_items (run_id,inbox_item_id,delivery_seq,attached_at,disposition) \
                 VALUES ($1,$2,$3,now(),$4) ON CONFLICT (run_id,inbox_item_id) DO UPDATE \
                 SET disposition=EXCLUDED.disposition",
            )
            .bind(run.id.into_uuid())
            .bind(item.inbox_item_id.into_uuid())
            .bind(i64::try_from(item.delivery_sequence).map_err(|_| ApplicationError::Conflict)?)
            .bind(item.disposition.map(disposition_str))
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        Ok(())
    }

    pub(super) async fn active_run_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError> {
        let value = sqlx::query_scalar::<_, Uuid>(
            "SELECT id FROM agent_runs WHERE agent_id = $1 \
             AND status NOT IN ('completed', 'yielded', 'failed', 'canceled') FOR UPDATE",
        )
        .bind(agent_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(value.map(RunId::from_uuid))
    }

    pub(super) async fn completed_run_for_event(
        &mut self,
        event_id: EventId,
    ) -> Result<Option<RunId>, ApplicationError> {
        let value = sqlx::query_scalar::<_, Uuid>(
            "SELECT run_id FROM run_result_events WHERE event_id = $1",
        )
        .bind(event_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(value.map(RunId::from_uuid))
    }

    pub(super) async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError> {
        let row = sqlx::query("SELECT * FROM agent_runs WHERE id = $1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        let item_rows = sqlx::query(
            "SELECT inbox_item_id, delivery_seq, disposition FROM run_items \
             WHERE run_id = $1 ORDER BY delivery_seq",
        )
        .bind(id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let items = item_rows
            .into_iter()
            .map(|item| {
                Ok(RunItemSnapshot {
                    inbox_item_id: InboxItemId::from_uuid(item.get("inbox_item_id")),
                    delivery_sequence: u64::try_from(item.get::<i64, _>("delivery_seq"))
                        .map_err(|_| ApplicationError::Internal)?,
                    disposition: item
                        .get::<Option<String>, _>("disposition")
                        .map(|value| disposition_from_str(&value))
                        .transpose()?,
                })
            })
            .collect::<Result<Vec<_>, ApplicationError>>()?;
        run_from_row(&row, items)
    }

    pub(super) async fn queue_command(
        &mut self,
        computer_id: ComputerId,
        command: Command,
    ) -> Result<(), ApplicationError> {
        let sequence: i64 = sqlx::query_scalar(
            "UPDATE computers SET next_command_seq=next_command_seq+1 \
             WHERE id=$1 AND deleted_at IS NULL RETURNING next_command_seq-1",
        )
        .bind(computer_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let kind = command_kind(&command);
        let payload = serde_json::to_value(&command).map_err(|_| ApplicationError::Internal)?;
        sqlx::query(
            "INSERT INTO computer_commands (id,computer_id,computer_seq,kind,payload_json,created_at) \
             VALUES ($1,$2,$3,$4,$5,now())",
        )
        .bind(CommandId::from_uuid(Uuid::now_v7()).into_uuid())
        .bind(computer_id.into_uuid())
        .bind(sequence)
        .bind(kind)
        .bind(payload)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn agent_configuration(
        &mut self,
        agent_id: MemberId,
    ) -> Result<crate::protocol::computer::AgentConfiguration, ApplicationError> {
        let row = sqlx::query(
            "SELECT agents.space_id,agents.role_text,agents.role_revision,agents.driver_kind, \
             members.display_name,members.handle FROM agents JOIN members \
             ON members.id=agents.member_id WHERE agents.member_id=$1",
        )
        .bind(agent_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(crate::protocol::computer::AgentConfiguration {
            agent_id: AgentId::from_uuid(agent_id.into_uuid()),
            space_id: SpaceId::from_uuid(row.get("space_id")),
            name: row.get("display_name"),
            handle: row.get("handle"),
            role: crate::protocol::computer::RoleSnapshot {
                revision: u64::try_from(row.get::<i64, _>("role_revision"))
                    .map_err(|_| ApplicationError::Internal)?,
                text: row.get("role_text"),
            },
            driver: match row.get::<&str, _>("driver_kind") {
                "codex" => crate::protocol::computer::DriverKind::Codex,
                "builtin" => crate::protocol::computer::DriverKind::Builtin,
                _ => return Err(ApplicationError::Internal),
            },
        })
    }

    pub(super) async fn computer_for_run(
        &mut self,
        run_id: RunId,
    ) -> Result<ComputerId, ApplicationError> {
        sqlx::query_scalar::<_, Uuid>(
            "SELECT agents.computer_id FROM agent_runs \
             JOIN agents ON agents.member_id=agent_runs.agent_id WHERE agent_runs.id=$1",
        )
        .bind(run_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map(ComputerId::from_uuid)
        .map_err(map_sqlx)
    }

    pub(super) async fn run_start(
        &mut self,
        run_id: RunId,
        fencing_token: &str,
    ) -> Result<RunStart, ApplicationError> {
        let row = sqlx::query(
            "SELECT agent_id,task_id,focus_thread_id,lease_expires_at FROM agent_runs WHERE id=$1",
        )
        .bind(run_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let agent_id = MemberId::from_uuid(row.get("agent_id"));
        let task_id = row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid);
        let focus_thread_id = ThreadId::from_uuid(row.get("focus_thread_id"));
        let item_ids = sqlx::query_scalar::<_, Uuid>(
            "SELECT inbox_item_id FROM run_items WHERE run_id=$1 ORDER BY delivery_seq",
        )
        .bind(run_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let mut claimed_items = Vec::with_capacity(item_ids.len());
        for item_id in item_ids {
            claimed_items.push(self.inbox_snapshot(InboxItemId::from_uuid(item_id)).await?);
        }
        Ok(RunStart {
            run_id,
            agent_id: AgentId::from_uuid(agent_id.into_uuid()),
            task: match task_id {
                Some(id) => Some(self.task_snapshot(id).await?),
                None => None,
            },
            focus: self.focus_snapshot(focus_thread_id).await?,
            claimed_items,
            fencing_token: FencingToken::new(fencing_token.to_owned()),
            ownership_lease_expires_at: row.get("lease_expires_at"),
        })
    }

    pub(super) async fn focus_snapshot(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<FocusSnapshot, ApplicationError> {
        let channel_id =
            sqlx::query_scalar::<_, Uuid>("SELECT channel_id FROM threads WHERE id=$1")
                .bind(thread_id.into_uuid())
                .fetch_one(&mut *self.connection)
                .await
                .map(ChannelId::from_uuid)
                .map_err(map_sqlx)?;
        let rows = sqlx::query(
            "SELECT id,author_member_id,channel_seq,content_kind,body_markdown,action_channel_id, \
             action_agent_member_id,created_at,placement FROM messages \
             WHERE thread_id=$1 AND deleted_at IS NULL ORDER BY channel_seq",
        )
        .bind(thread_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let message_sequence = rows
            .last()
            .map(|row| u64::try_from(row.get::<i64, _>("channel_seq")))
            .transpose()
            .map_err(|_| ApplicationError::Internal)?
            .unwrap_or(0);
        let mut root = None;
        let mut replies = Vec::new();
        for row in rows {
            let placement: String = row.get("placement");
            let snapshot = wire_message(&row)?;
            if placement == "root" {
                root = Some(snapshot);
            } else {
                replies.push(snapshot);
            }
        }
        Ok(FocusSnapshot {
            thread_id,
            channel_id,
            root: root.ok_or(ApplicationError::Internal)?,
            replies,
            message_sequence,
        })
    }
}
#[async_trait]
impl ExecutionTransaction for PostgresTransaction {
    async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError> {
        self.run(id).await
    }
    async fn active_run_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError> {
        self.active_run_for_agent(agent_id).await
    }
    async fn completed_run_for_event(
        &mut self,
        event_id: EventId,
    ) -> Result<Option<RunId>, ApplicationError> {
        self.completed_run_for_event(event_id).await
    }
    async fn runs_with_expired_lease(
        &mut self,
        now: time::OffsetDateTime,
        limit: u32,
    ) -> Result<Vec<RunId>, ApplicationError> {
        self.runs_with_expired_lease(now, limit).await
    }
    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError> {
        self.save_run(run).await
    }
    async fn next_claim_candidate(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<ClaimCandidate>, ApplicationError> {
        self.next_claim_candidate(computer_id).await
    }
    async fn record_claim_failure(
        &mut self,
        item_id: InboxItemId,
        message_id: Option<MessageId>,
        channel_id: ChannelId,
        error_code: &str,
    ) -> Result<bool, ApplicationError> {
        self.record_claim_failure(item_id, message_id, channel_id, error_code)
            .await
    }
    async fn authorize_run_capability(
        &mut self,
        proof: &RunCapabilityProof,
    ) -> Result<bool, ApplicationError> {
        self.authorize_run_capability(proof).await
    }
    async fn active_run_for_visible_agent(
        &mut self,
        agent_id: MemberId,
        viewer_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError> {
        self.active_run_for_visible_agent(agent_id, viewer_id).await
    }
    async fn agent_provision_command_target(
        &mut self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<Option<MemberId>, ApplicationError> {
        self.agent_provision_command_target(computer_id, command_id, sequence)
            .await
    }
}
