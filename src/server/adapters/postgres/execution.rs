use super::*;
use crate::protocol::computer::{
    AgentConfiguration, ChannelMemberSnapshot, DriverKind, RoleSnapshot,
};

/// Maps the Inbox Item kind that produced the work onto the Run's Trigger. Both `direct` and `reply`
/// arrive as explicit addressing, which is what `mention` and `direct_message` record.
fn trigger_for_item_kind(kind: &str) -> Result<RunTrigger, ApplicationError> {
    match kind {
        "direct" => Ok(RunTrigger::DirectMessage),
        "mention" | "reply" | "system" => Ok(RunTrigger::Mention),
        "task_activity" => Ok(RunTrigger::TaskActivity),
        "thread_activity" => Ok(RunTrigger::ThreadActivity),
        "channel_activity" => Ok(RunTrigger::ChannelActivity),
        _ => Err(ApplicationError::Internal),
    }
}

pub(super) fn command_kind(command: &Command) -> &'static str {
    match command {
        Command::AgentProvision(_) => "agent.provision",
        Command::AgentConfigure(_) => "agent.configure",
        Command::AgentSuspend(_) => "agent.suspend",
        Command::AgentResume(_) => "agent.resume",
        Command::AgentRestart(_) => "agent.restart",
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
        let id: Option<Uuid> = sqlx::query_scalar("SELECT r.id FROM agent_runs r JOIN messages t ON t.id=r.focus_thread_id AND t.placement='root' JOIN channel_members cm ON cm.channel_id=t.channel_id AND cm.member_id=$2 WHERE r.agent_id=$1 AND r.status NOT IN ('completed','yielded','failed','canceled') ORDER BY r.created_at DESC LIMIT 1")
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
               AND r.focus_thread_id=$5 AND r.status='working' AND a.computer_id=$6)",
        )
        .bind(proof.run_id.into_uuid())
        .bind(proof.agent_id.into_uuid())
        .bind(proof.space_id.into_uuid())
        .bind(proof.task_id.map(TaskId::into_uuid))
        .bind(proof.focus_thread_id.into_uuid())
        .bind(proof.computer_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }
    /// Finds Agents with available work and no live Run, newest-starving first. Returns at most one
    /// row per Agent, because the partial unique index allows one live Run per Agent.
    pub(super) async fn dispatchable_work(
        &mut self,
        now: OffsetDateTime,
        limit: u32,
    ) -> Result<Vec<DispatchCandidate>, ApplicationError> {
        let rows = sqlx::query(
            "SELECT DISTINCT ON (i.member_id) \
               i.id,i.member_id,i.task_id,i.thread_id,i.message_id,i.kind,a.computer_id,t.channel_id \
             FROM inbox_items i \
             JOIN agents a ON a.member_id=i.member_id \
             JOIN messages t ON t.id=i.thread_id AND t.placement='root' \
             WHERE a.lifecycle='active' AND a.computer_id IS NOT NULL \
               AND i.status='pending' AND i.available_at<=$1 \
               AND EXISTS(SELECT 1 FROM channel_members cm \
                         WHERE cm.channel_id=t.channel_id AND cm.member_id=i.member_id) \
               AND NOT EXISTS(SELECT 1 FROM agent_runs r WHERE r.agent_id=i.member_id \
                 AND r.status NOT IN ('completed','yielded','failed','canceled')) \
             ORDER BY i.member_id,(i.strength='hard') DESC,i.available_at,(i.task_id IS NOT NULL) DESC,i.id \
             LIMIT $2",
        )
        .bind(now)
        .bind(i64::from(limit))
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        rows.into_iter()
            .map(|row| {
                Ok(DispatchCandidate {
                    item_id: InboxItemId::from_uuid(row.get("id")),
                    agent_id: MemberId::from_uuid(row.get("member_id")),
                    computer_id: ComputerId::from_uuid(row.get("computer_id")),
                    task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
                    thread_id: ThreadId::from_uuid(row.get("thread_id")),
                    message_id: row
                        .get::<Option<Uuid>, _>("message_id")
                        .map(MessageId::from_uuid),
                    channel_id: ChannelId::from_uuid(row.get("channel_id")),
                    trigger: trigger_for_item_kind(row.get("kind"))?,
                })
            })
            .collect()
    }

    pub(super) async fn record_dispatch_failure(
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
    /// Runs the Server believes are live on this Computer. Compared against what the Computer reports
    /// on reconnect: anything the Computer no longer holds died with its previous daemon process.
    pub(super) async fn nonterminal_runs_for_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Vec<RunId>, ApplicationError> {
        sqlx::query_scalar::<_, Uuid>(
            "SELECT r.id FROM agent_runs r JOIN agents a ON a.member_id=r.agent_id \
             WHERE a.computer_id=$1 \
               AND r.status NOT IN ('completed','yielded','failed','canceled') \
               AND NOT (r.status='dispatched' AND EXISTS(\
                 SELECT 1 FROM computer_commands c \
                  WHERE c.computer_id=$1 AND c.kind='run.start' AND c.acked_at IS NULL \
                    AND c.payload_json #>> '{payload,run_id}' = r.id::text\
               )) \
             ORDER BY r.created_at,r.id",
        )
        .bind(computer_id.into_uuid())
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
             trigger_kind,cancel_requested,outcome_code,error_code,continuation_note,created_at,started_at,finished_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now(),$12,$13) \
             ON CONFLICT (id) DO UPDATE SET task_id=EXCLUDED.task_id,status=EXCLUDED.status, \
             cancel_requested=EXCLUDED.cancel_requested,outcome_code=EXCLUDED.outcome_code, \
             error_code=EXCLUDED.error_code, \
             continuation_note=EXCLUDED.continuation_note,started_at=EXCLUDED.started_at,finished_at=EXCLUDED.finished_at",
        )
        .bind(run.id.into_uuid())
        .bind(run.space_id.into_uuid())
        .bind(run.agent_id.into_uuid())
        .bind(run.task_id.map(TaskId::into_uuid))
        .bind(run.focus_thread_id.into_uuid())
        .bind(run_status_str(run.status))
        .bind(run_trigger_str(run.trigger))
        .bind(run.cancel_requested)
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

    pub(super) async fn observed_thread_sequence(
        &mut self,
        run_id: RunId,
    ) -> Result<Option<u64>, ApplicationError> {
        let sequence: Option<Option<i64>> =
            sqlx::query_scalar("SELECT observed_thread_seq FROM agent_runs WHERE id=$1")
                .bind(run_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        sequence
            .flatten()
            .map(|value| u64::try_from(value).map_err(|_| ApplicationError::Internal))
            .transpose()
    }

    pub(super) async fn record_observed_thread_sequence(
        &mut self,
        run_id: RunId,
        sequence: u64,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "UPDATE agent_runs SET observed_thread_seq=GREATEST(observed_thread_seq,$2) WHERE id=$1",
        )
        .bind(run_id.into_uuid())
        .bind(i64::try_from(sequence).map_err(|_| ApplicationError::Conflict)?)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
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
        self.notified_computers.insert(computer_id.into_uuid());
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
    ) -> Result<AgentConfiguration, ApplicationError> {
        let row = sqlx::query(
            "SELECT agents.space_id,agents.role_text,agents.role_revision,agents.driver_kind, \
             members.display_name FROM agents JOIN members \
             ON members.id=agents.member_id WHERE agents.member_id=$1",
        )
        .bind(agent_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(AgentConfiguration {
            agent_id: AgentId::from_uuid(agent_id.into_uuid()),
            space_id: SpaceId::from_uuid(row.get("space_id")),
            name: row.get("display_name"),
            role: RoleSnapshot {
                revision: u64::try_from(row.get::<i64, _>("role_revision"))
                    .map_err(|_| ApplicationError::Internal)?,
                text: row.get("role_text"),
            },
            driver: match row.get::<&str, _>("driver_kind") {
                "codex" => DriverKind::Codex,
                "builtin" => DriverKind::Builtin,
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

    pub(super) async fn run_start(&mut self, run_id: RunId) -> Result<RunStart, ApplicationError> {
        let row =
            sqlx::query("SELECT agent_id,task_id,focus_thread_id FROM agent_runs WHERE id=$1")
                .bind(run_id.into_uuid())
                .fetch_one(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        let agent_id = MemberId::from_uuid(row.get("agent_id"));
        let task_id = row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid);
        let focus_thread_id = ThreadId::from_uuid(row.get("focus_thread_id"));
        let channel_members = sqlx::query_as::<_, (Uuid, String)>(
            "SELECT members.id,members.display_name FROM messages \
             JOIN channel_members ON channel_members.channel_id=messages.channel_id \
             JOIN members ON members.id=channel_members.member_id \
             WHERE messages.id=$1 AND messages.placement='root' \
               AND members.retired_at IS NULL \
             ORDER BY members.display_name,members.id",
        )
        .bind(focus_thread_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        .into_iter()
        .map(|(member_id, display_name)| ChannelMemberSnapshot {
            member_id: MemberId::from_uuid(member_id),
            display_name,
        })
        .collect();
        let item_ids = sqlx::query_scalar::<_, Uuid>(
            "SELECT inbox_item_id FROM run_items WHERE run_id=$1 ORDER BY delivery_seq",
        )
        .bind(run_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let mut dispatched_items = Vec::with_capacity(item_ids.len());
        for item_id in item_ids {
            dispatched_items.push(self.inbox_snapshot(InboxItemId::from_uuid(item_id)).await?);
        }
        Ok(RunStart {
            run_id,
            agent_id: AgentId::from_uuid(agent_id.into_uuid()),
            task: match task_id {
                Some(id) => Some(self.task_snapshot(id).await?),
                None => None,
            },
            focus: self.focus_snapshot(focus_thread_id).await?,
            dispatched_items,
            channel_members,
        })
    }

    pub(super) async fn focus_snapshot(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<FocusSnapshot, ApplicationError> {
        let channel_id = sqlx::query_scalar::<_, Uuid>(
            "SELECT channel_id FROM messages WHERE id=$1 AND placement='root'",
        )
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
    async fn nonterminal_runs_for_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Vec<RunId>, ApplicationError> {
        self.nonterminal_runs_for_computer(computer_id).await
    }
    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError> {
        self.save_run(run).await
    }
    async fn observed_thread_sequence(
        &mut self,
        run_id: RunId,
    ) -> Result<Option<u64>, ApplicationError> {
        self.observed_thread_sequence(run_id).await
    }
    async fn record_observed_thread_sequence(
        &mut self,
        run_id: RunId,
        sequence: u64,
    ) -> Result<(), ApplicationError> {
        self.record_observed_thread_sequence(run_id, sequence).await
    }
    async fn dispatchable_work(
        &mut self,
        now: time::OffsetDateTime,
        limit: u32,
    ) -> Result<Vec<DispatchCandidate>, ApplicationError> {
        self.dispatchable_work(now, limit).await
    }
    async fn record_dispatch_failure(
        &mut self,
        item_id: InboxItemId,
        message_id: Option<MessageId>,
        channel_id: ChannelId,
        error_code: &str,
    ) -> Result<bool, ApplicationError> {
        self.record_dispatch_failure(item_id, message_id, channel_id, error_code)
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
