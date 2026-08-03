use std::collections::BTreeSet;

use async_trait::async_trait;
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Postgres, Row, pool::PoolConnection};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::{
    ids::{
        AgentId, AttachmentId, ChannelId, CommandId, ComputerId, EventId, IdempotencyKey,
        InboxItemId, MemberId, MessageId, NoticeId, RunId, SpaceId, TaskId, ThreadId,
    },
    protocol::computer::{
        ActionKind, ActionTarget, AgentRetire, AttentionNotice,
        AttentionStrength as WireAttentionStrength, Command, CommandAck, CommandEnvelope,
        CommandSequence, DeliverySequence, FocusSnapshot, InboxItemSnapshot, InboxSourceKind,
        MessageContent as WireMessageContent, MessageSnapshot, NoticeLocation, RunAttachItem,
        RunNotice, RunStart, RunStop, RunTaskBound, SessionChangeReason, SessionCommand,
        SessionScope, StopReason, TaskSnapshot, TaskStatus as WireTaskStatus,
    },
    server::{
        application::ports::{
            ApplicationError, AttachmentTransaction, AuthenticatedHuman, CollaborationTransaction,
            ComputerRecord, CreatedSpace, DirectMessageView, DispatchCandidate, Effect, EffectSink,
            ExecutionTransaction, HumanMemberRecord, IdentityTransaction, InboxItemView,
            InboxScope, MemberKind, MessageDraft, PairedComputer, PublishedMessage,
            RunCapabilityProof, SpaceHumanMember, SpaceMemberView, TaskTransaction,
            TransactionPort,
        },
        domain::{
            access::{HumanRegistration, SpaceAccess},
            attachment::{Attachment, AttachmentSnapshot, AttachmentStatus},
            attention::{
                AmbientAggregateSnapshot, AttentionStrength, InboxItem, InboxItemDisposition,
                InboxItemKind, InboxItemSnapshot as DomainInboxItemSnapshot, InboxItemStatus,
            },
            conversation::{
                Channel, ChannelKind, Message, MessageContent, MessagePlacement, Thread,
            },
            execution::{
                Run, RunErrorCode, RunItemSnapshot, RunOutcome, RunSnapshot as DomainRunSnapshot,
                RunStatus, RunTrigger,
            },
            identity::{
                AccessLevel, Agent, AgentLifecycle, Computer, ComputerLifecycle, DriverKind,
                Member, PermissionAction,
            },
            invitation::{Invitation, InvitationDraft, InvitationStatus},
            pairing::{ComputerOs, Pairing, PairingRequest, PairingStatus},
            task::{
                CloseReason, RelatedThreadSnapshot, Task, TaskSnapshot as DomainTaskSnapshot,
                TaskStatus,
            },
        },
    },
};

const BASELINE: &str = include_str!("../../../../schema/postgres.sql");

#[derive(Clone)]
pub(super) struct PostgresAdapter {
    pool: PgPool,
}

pub(super) struct PostgresTransaction {
    connection: PoolConnection<Postgres>,
    effects: Vec<Effect>,
}

impl PostgresAdapter {
    pub(super) fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    pub(super) async fn initialize_schema(&self) -> Result<(), sqlx::Error> {
        let mut transaction = self.pool.begin().await?;
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended('sumi-schema-baseline', 0))")
            .execute(&mut *transaction)
            .await?;
        let initialized: bool =
            sqlx::query_scalar("SELECT to_regclass('public.schema_meta') IS NOT NULL")
                .fetch_one(&mut *transaction)
                .await?;
        if !initialized {
            sqlx::raw_sql(BASELINE).execute(&mut *transaction).await?;
        }
        let version: i32 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&mut *transaction)
            .await?;
        if version < 3 {
            if version != 2 {
                return Err(sqlx::Error::Protocol(format!(
                    "unsupported schema baseline version {version}"
                )));
            }
            sqlx::raw_sql(
                "ALTER TABLE spaces ADD COLUMN accent TEXT NOT NULL DEFAULT '#FE7DA8'; \
                 ALTER TABLE spaces ALTER COLUMN accent DROP DEFAULT; \
                 INSERT INTO schema_meta (version, applied_at) VALUES (3, now());",
            )
            .execute(&mut *transaction)
            .await?;
        }
        let version: i32 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&mut *transaction)
            .await?;
        if version < 5 {
            if version != 4 {
                return Err(sqlx::Error::Protocol(format!(
                    "unsupported schema baseline version {version}"
                )));
            }
            sqlx::raw_sql(
                "ALTER TABLE messages DROP CONSTRAINT messages_content_kind_check; \
                 ALTER TABLE messages ADD CONSTRAINT messages_content_kind_check CHECK (content_kind IN ('text', 'channel_created', 'agent_created', 'system_notice')); \
                 ALTER TABLE messages DROP CONSTRAINT messages_check; \
                 ALTER TABLE messages ADD CONSTRAINT messages_check CHECK (\
                    (content_kind = 'text' AND body_markdown IS NOT NULL AND action_channel_id IS NULL AND action_agent_member_id IS NULL)\
                    OR (content_kind = 'channel_created' AND placement = 'reply' AND body_markdown IS NULL AND action_channel_id IS NOT NULL AND action_agent_member_id IS NULL)\
                    OR (content_kind = 'agent_created' AND placement = 'reply' AND body_markdown IS NULL AND action_channel_id IS NULL AND action_agent_member_id IS NOT NULL)\
                    OR (content_kind = 'system_notice' AND body_markdown IS NOT NULL AND action_channel_id IS NULL AND action_agent_member_id IS NULL)\
                 ); \
                 INSERT INTO schema_meta (version, applied_at) VALUES (5, now());",
            )
            .execute(&mut *transaction)
            .await?;
        }
        transaction.commit().await
    }

    pub(super) async fn pending_commands(
        &self,
        computer_id: ComputerId,
        watermark: CommandSequence,
    ) -> Result<Vec<CommandEnvelope>, ApplicationError> {
        let watermark = i64::try_from(watermark.0).map_err(|_| ApplicationError::Conflict)?;
        let rows = sqlx::query(
            "SELECT id,computer_seq,payload_json FROM computer_commands \
             WHERE computer_id=$1 AND computer_seq>$2 AND acked_at IS NULL ORDER BY computer_seq",
        )
        .bind(computer_id.into_uuid())
        .bind(watermark)
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)?;
        rows.into_iter()
            .map(|row| {
                let sequence = u64::try_from(row.get::<i64, _>("computer_seq"))
                    .map_err(|_| ApplicationError::Internal)?;
                let command = serde_json::from_value(row.get("payload_json"))
                    .map_err(|_| ApplicationError::Internal)?;
                Ok(CommandEnvelope {
                    command_id: CommandId::from_uuid(row.get("id")),
                    sequence: CommandSequence(sequence),
                    command,
                })
            })
            .collect()
    }

    pub(super) async fn acknowledge_command(
        &self,
        computer_id: ComputerId,
        ack: &CommandAck,
    ) -> Result<(), ApplicationError> {
        let sequence = i64::try_from(ack.sequence.0).map_err(|_| ApplicationError::Conflict)?;
        let changed = sqlx::query(
            "UPDATE computer_commands SET acked_at=COALESCE(acked_at,now()) \
             WHERE id=$1 AND computer_id=$2 AND computer_seq=$3",
        )
        .bind(ack.command_id.into_uuid())
        .bind(computer_id.into_uuid())
        .bind(sequence)
        .execute(&self.pool)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            Ok(())
        } else {
            Err(ApplicationError::ContextChanged)
        }
    }

    /// Reads the Space event window as one viewer sees it. `viewer_member_id` filters events whose
    /// payload names a Channel the viewer cannot read, and Inbox events belonging to another
    /// Member, so a private Channel does not leak through the stream.
    pub(super) async fn browser_events(
        &self,
        space_id: SpaceId,
        viewer_member_id: MemberId,
        last_event_id: Option<EventId>,
    ) -> Result<Option<Vec<super::realtime::BrowserEvent<serde_json::Value>>>, ApplicationError>
    {
        let cursor = if let Some(event_id) = last_event_id {
            let cursor = sqlx::query_as::<_, (time::OffsetDateTime, Uuid)>(
                "SELECT created_at,id FROM outbox_events WHERE id=$1 AND space_id=$2",
            )
            .bind(event_id.into_uuid())
            .bind(space_id.into_uuid())
            .fetch_optional(&self.pool)
            .await
            .map_err(map_sqlx)?;
            let Some(cursor) = cursor else {
                return Ok(None);
            };
            Some(cursor)
        } else {
            None
        };
        let rows = if let Some((created_at, id)) = cursor {
            sqlx::query(
                "SELECT id,kind,payload_json,created_at FROM outbox_events \
                 WHERE space_id=$1 AND (created_at,id)>($2,$3) ORDER BY created_at,id LIMIT 100",
            )
            .bind(space_id.into_uuid())
            .bind(created_at)
            .bind(id)
            .fetch_all(&self.pool)
            .await
            .map_err(map_sqlx)?
        } else {
            sqlx::query(
                "SELECT id,kind,payload_json,created_at FROM outbox_events \
                 WHERE space_id=$1 ORDER BY created_at,id LIMIT 100",
            )
            .bind(space_id.into_uuid())
            .fetch_all(&self.pool)
            .await
            .map_err(map_sqlx)?
        };
        let readable_channels = sqlx::query_scalar::<_, Uuid>(
            "SELECT channel_id FROM channel_members WHERE member_id=$1",
        )
        .bind(viewer_member_id.into_uuid())
        .fetch_all(&self.pool)
        .await
        .map_err(map_sqlx)?
        .into_iter()
        .collect::<std::collections::HashSet<_>>();
        // Governors read Agent Inboxes, so they must also receive their `inbox.changed`.
        let governs_space = sqlx::query_scalar::<_, bool>(
            "SELECT access_level IN ('owner','admin') FROM members WHERE id=$1",
        )
        .bind(viewer_member_id.into_uuid())
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)?
        .unwrap_or(false);
        Ok(Some(
            rows.into_iter()
                .map(|row| super::realtime::BrowserEvent {
                    event_id: EventId::from_uuid(row.get("id")),
                    event_type: row.get("kind"),
                    space_id,
                    occurred_at: row.get("created_at"),
                    data: row.get::<serde_json::Value, _>("payload_json"),
                })
                .filter(|event| {
                    event_is_visible(
                        &event.event_type,
                        &event.data,
                        viewer_member_id,
                        governs_space,
                        &readable_channels,
                    )
                })
                .collect(),
        ))
    }

    /// Best-effort insert of an ephemeral `agent.activity` event for the Browser feed. A failed
    /// insert must never fail the Agent action that produced the activity.
    pub(in crate::server::adapters) async fn record_agent_activity(
        &self,
        space_id: SpaceId,
        agent_member_id: MemberId,
        kind: &str,
        payload: serde_json::Value,
    ) {
        let mut record = payload;
        if let Some(object) = record.as_object_mut() {
            object.insert(
                "member_id".to_owned(),
                serde_json::json!(agent_member_id.into_uuid()),
            );
            object.insert("kind".to_owned(), serde_json::json!(kind));
        } else {
            record = serde_json::json!({
                "member_id": agent_member_id.into_uuid(),
                "kind": kind,
            });
        }
        // Semantic arguments and previews can contain bounded user text, so both require a
        // Channel scope. Keep the action fact if a caller forgot the scope, but drop its details.
        let has_channel_scope = ["channel_id", "scope_channel_id"].iter().any(|field| {
            record
                .get(*field)
                .and_then(serde_json::Value::as_str)
                .and_then(|value| value.parse::<Uuid>().ok())
                .is_some()
        });
        if !has_channel_scope && let Some(object) = record.as_object_mut() {
            object.remove("arguments");
            object.remove("message_preview");
            object.remove("message_truncated");
        }
        let result = sqlx::query(
            "INSERT INTO outbox_events (id,space_id,kind,payload_json,created_at) \
             VALUES ($1,$2,'agent.activity',$3,now())",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(record)
        .execute(&self.pool)
        .await;
        if let Err(error) = result {
            tracing::warn!(
                agent_member_id = %agent_member_id.into_uuid(),
                kind,
                error = %error,
                "agent.activity event could not be recorded"
            );
        }
    }
}

/// Decides whether one event may reach a viewer. An event that names no Channel and no Member
/// carries only a resource ID the viewer already reaches through an authorized read, so it passes.
fn event_is_visible(
    kind: &str,
    payload: &serde_json::Value,
    viewer_member_id: MemberId,
    governs_space: bool,
    readable_channels: &std::collections::HashSet<Uuid>,
) -> bool {
    let payload_uuid = |field: &str| {
        payload
            .get(field)
            .and_then(|value| value.as_str())
            .and_then(|value| value.parse::<Uuid>().ok())
    };
    let visibility_channel =
        payload_uuid("scope_channel_id").or_else(|| payload_uuid("channel_id"));
    if let Some(channel_id) = visibility_channel
        && !readable_channels.contains(&channel_id)
    {
        return false;
    }
    if kind != "agent.activity"
        && !(kind == "member.changed" && payload_uuid("channel_id").is_some())
        && let Some(member_id) = payload_uuid("member_id")
        && member_id != viewer_member_id.into_uuid()
        && !governs_space
    {
        return false;
    }
    true
}

impl TransactionPort for PostgresAdapter {
    type Transaction = PostgresTransaction;

    async fn transact<T>(
        &mut self,
        operation: impl for<'a> AsyncFnOnce(&'a mut Self::Transaction) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError> {
        let mut transaction = PostgresTransaction {
            connection: self
                .pool
                .acquire()
                .await
                .map_err(|_| ApplicationError::Unavailable)?,
            effects: Vec::new(),
        };
        sqlx::query("BEGIN")
            .execute(&mut *transaction.connection)
            .await
            .map_err(map_sqlx)?;
        match operation(&mut transaction).await {
            Ok(value) => {
                if let Err(error) = transaction.flush_effects().await {
                    transaction.rollback().await;
                    return Err(error);
                }
                if let Err(error) = sqlx::query("COMMIT")
                    .execute(&mut *transaction.connection)
                    .await
                    .map_err(map_sqlx)
                {
                    transaction.rollback().await;
                    return Err(error);
                }
                Ok(value)
            }
            Err(error) => {
                transaction.rollback().await;
                Err(error)
            }
        }
    }
}

mod attachment;
mod attention;
mod conversation;
mod execution;
mod identity;
mod rows;
mod task;

use rows::*;

impl PostgresTransaction {
    async fn space_of_member(
        &mut self,
        member_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        Ok(
            sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM members WHERE id=$1")
                .bind(member_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?
                .map(SpaceId::from_uuid),
        )
    }

    async fn lock_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<(), ApplicationError> {
        let lock_key = format!("{}:{action}:{}", actor.into_uuid(), key.into_uuid());
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(lock_key)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    async fn resource_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<Uuid>, ApplicationError> {
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(format!("{}:{action}:{}", actor, key))
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query_scalar(
            "SELECT resource_id FROM idempotency_records \
             WHERE actor_member_id=$1 AND action=$2 AND idempotency_key=$3",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    async fn record_resource_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        resource_id: Uuid,
    ) -> Result<(), ApplicationError> {
        let hash = Sha256::digest(resource_id.as_bytes());
        sqlx::query(
            "INSERT INTO idempotency_records (actor_member_id,action,idempotency_key,response_code, \
             resource_id,result_hash,created_at) VALUES ($1,$2,$3,'ok',$4,$5,now())",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .bind(resource_id)
        .bind(hash.as_slice())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn rollback(&mut self) {
        let _ = sqlx::query("ROLLBACK").execute(&mut *self.connection).await;
    }

    async fn flush_effects(&mut self) -> Result<(), ApplicationError> {
        for effect in std::mem::take(&mut self.effects) {
            let (space_id, kind, payload) = self.effect_record(effect).await?;
            sqlx::query(
                "INSERT INTO outbox_events (id,space_id,kind,payload_json,created_at) \
                 VALUES ($1,$2,$3,$4,now())",
            )
            .bind(Uuid::now_v7())
            .bind(space_id.into_uuid())
            .bind(kind)
            .bind(payload)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        Ok(())
    }

    async fn effect_record(
        &mut self,
        effect: Effect,
    ) -> Result<(SpaceId, &'static str, serde_json::Value), ApplicationError> {
        let (kind, subject_id) = match effect {
            Effect::MessageCreated(id) => ("message.created", id.into_uuid()),
            Effect::MessageUpdated(id) => ("message.updated", id.into_uuid()),
            Effect::MessageDeleted(id) => ("message.deleted", id.into_uuid()),
            Effect::TaskCreated(id) => ("task.created", id.into_uuid()),
            Effect::RunTaskBound { run_id, task_id } => {
                let computer_id = self.computer_for_run(run_id).await?;
                let task = self.task_snapshot(task_id).await?;
                self.queue_command(
                    computer_id,
                    Command::RunTaskBound(RunTaskBound { run_id, task }),
                )
                .await?;
                return Ok((
                    self.space_for_run(run_id).await?,
                    "run.task_bound",
                    json!({"run_id": run_id, "task_id": task_id}),
                ));
            }
            Effect::ThreadLinked { task_id, thread_id } => {
                return Ok((
                    self.space_for_task(task_id).await?,
                    "task.linked",
                    json!({"task_id": task_id, "thread_id": thread_id}),
                ));
            }
            Effect::ThreadUnlinked { task_id, thread_id } => {
                return Ok((
                    self.space_for_task(task_id).await?,
                    "task.unlinked",
                    json!({"task_id": task_id, "thread_id": thread_id}),
                ));
            }
            Effect::ItemAttached {
                run_id,
                item_id,
                sequence,
            } => {
                let computer_id = self.computer_for_run(run_id).await?;
                let item = self.inbox_snapshot(item_id).await?;
                self.queue_command(
                    computer_id,
                    Command::RunAttachItem(RunAttachItem {
                        run_id,
                        delivery_sequence: DeliverySequence(sequence),
                        item,
                    }),
                )
                .await?;
                return Ok((
                    self.space_for_run(run_id).await?,
                    "run.item_attached",
                    json!({"run_id": run_id, "item_id": item_id, "delivery_sequence": sequence}),
                ));
            }
            Effect::RunNotice {
                run_id,
                item_id,
                location_visible,
            } => {
                let computer_id = self.computer_for_run(run_id).await?;
                let notice = self.attention_notice(item_id, location_visible).await?;
                self.queue_command(
                    computer_id,
                    Command::RunNotice(RunNotice { run_id, notice }),
                )
                .await?;
                return Ok((
                    self.space_for_run(run_id).await?,
                    "run.notice",
                    json!({"run_id": run_id, "notice_id": item_id}),
                ));
            }
            Effect::RunDispatched(run_id) => {
                let computer_id = self.computer_for_run(run_id).await?;
                let command = self.run_start(run_id).await?;
                self.queue_command(computer_id, Command::RunStart(command))
                    .await?;
                ("run.changed", run_id.into_uuid())
            }
            Effect::RunCancelRequested(run_id) => {
                let computer_id = self.computer_for_run(run_id).await?;
                self.queue_command(
                    computer_id,
                    Command::RunStop(RunStop {
                        run_id,
                        reason: StopReason::HumanRequest,
                    }),
                )
                .await?;
                ("run.changed", run_id.into_uuid())
            }
            Effect::RunStarted(id) => ("run.changed", id.into_uuid()),
            Effect::RunCompleted(id) => ("run.changed", id.into_uuid()),
            Effect::TaskCompleted {
                task_id,
                result_message_id,
            } => {
                return Ok((
                    self.space_for_task(task_id).await?,
                    "task.finished",
                    json!({"task_id": task_id, "result_message_id": result_message_id}),
                ));
            }
            Effect::TaskFinished(id) => ("task.finished", id.into_uuid()),
            Effect::SessionClose(id) => {
                if let Some((agent_id, computer_id)) = self.task_assignment(id).await? {
                    self.queue_command(
                        computer_id,
                        Command::SessionClose(SessionCommand {
                            agent_id: AgentId::from_uuid(agent_id.into_uuid()),
                            scope: SessionScope::Task(id),
                            reason: SessionChangeReason::TaskFinished,
                        }),
                    )
                    .await?;
                }
                ("session.close", id.into_uuid())
            }
            Effect::SessionReset(id) => {
                if let Some((agent_id, computer_id)) = self.task_assignment(id).await? {
                    self.queue_command(
                        computer_id,
                        Command::SessionReset(SessionCommand {
                            agent_id: AgentId::from_uuid(agent_id.into_uuid()),
                            scope: SessionScope::Task(id),
                            reason: SessionChangeReason::ExplicitReset,
                        }),
                    )
                    .await?;
                }
                ("session.reset", id.into_uuid())
            }
            Effect::AgentRetired {
                agent_id,
                computer_id,
            } => {
                self.queue_command(
                    computer_id,
                    Command::AgentRetire(AgentRetire {
                        agent_id: AgentId::from_uuid(agent_id.into_uuid()),
                    }),
                )
                .await?;
                ("agent.changed", agent_id.into_uuid())
            }
            Effect::ComputerDeleted(id) => ("computer.changed", id.into_uuid()),
            Effect::TaskUpdated(id) => ("task.updated", id.into_uuid()),
            Effect::ChannelCreated(id) => ("channel.created", id.into_uuid()),
            Effect::ChannelUpdated(id) => ("channel.updated", id.into_uuid()),
            Effect::AgentUpdated(id) => ("agent.updated", id.into_uuid()),
            Effect::AgentCreated {
                agent_id,
                computer_id,
            } => {
                let configuration = self.agent_configuration(agent_id).await?;
                self.queue_command(computer_id, Command::AgentProvision(configuration))
                    .await?;
                ("agent.created", agent_id.into_uuid())
            }
            Effect::PermissionChanged(id) => ("member.changed", id.into_uuid()),
            Effect::InboxChanged(member_id) => {
                let space_id =
                    sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM members WHERE id=$1")
                        .bind(member_id.into_uuid())
                        .fetch_one(&mut *self.connection)
                        .await
                        .map_err(map_sqlx)?;
                return Ok((
                    SpaceId::from_uuid(space_id),
                    "inbox.changed",
                    json!({"member_id": member_id}),
                ));
            }
            Effect::ThreadUpdated(thread_id) => {
                let row = sqlx::query(
                    "SELECT space_id,channel_id FROM messages \
                     WHERE id=$1 AND placement='root'",
                )
                .bind(thread_id.into_uuid())
                .fetch_one(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
                return Ok((
                    SpaceId::from_uuid(row.get("space_id")),
                    "thread.updated",
                    json!({
                        "resource_id": thread_id,
                        "channel_id": row.get::<Uuid, _>("channel_id"),
                    }),
                ));
            }
        };
        if kind.starts_with("message.") {
            let row = sqlx::query("SELECT space_id,channel_id FROM messages WHERE id=$1")
                .bind(subject_id)
                .fetch_one(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
            return Ok((
                SpaceId::from_uuid(row.get("space_id")),
                kind,
                json!({"resource_id": subject_id, "channel_id": row.get::<Uuid,_>("channel_id")}),
            ));
        }
        let space_id = self.space_for_subject(kind, subject_id).await?;
        Ok((space_id, kind, json!({"resource_id": subject_id})))
    }

    async fn space_for_run(&mut self, id: RunId) -> Result<SpaceId, ApplicationError> {
        self.space_by_query(
            "SELECT space_id FROM agent_runs WHERE id=$1",
            id.into_uuid(),
        )
        .await
    }

    async fn space_for_task(&mut self, id: TaskId) -> Result<SpaceId, ApplicationError> {
        self.space_by_query("SELECT space_id FROM tasks WHERE id=$1", id.into_uuid())
            .await
    }

    async fn space_for_subject(
        &mut self,
        kind: &str,
        id: Uuid,
    ) -> Result<SpaceId, ApplicationError> {
        let query =
            if kind.starts_with("task.") || matches!(kind, "session.close" | "session.reset") {
                "SELECT space_id FROM tasks WHERE id=$1"
            } else if kind.starts_with("message.") {
                "SELECT space_id FROM messages WHERE id=$1"
            } else if kind.starts_with("run.") {
                "SELECT space_id FROM agent_runs WHERE id=$1"
            } else if kind.starts_with("agent.") {
                "SELECT space_id FROM agents WHERE member_id=$1"
            } else if kind.starts_with("member.") {
                "SELECT space_id FROM members WHERE id=$1"
            } else if kind.starts_with("computer.") {
                "SELECT space_id FROM computers WHERE id=$1"
            } else {
                "SELECT space_id FROM channels WHERE id=$1"
            };
        self.space_by_query(query, id).await
    }

    async fn space_by_query(&mut self, query: &str, id: Uuid) -> Result<SpaceId, ApplicationError> {
        sqlx::query_scalar::<_, Uuid>(query)
            .bind(id)
            .fetch_one(&mut *self.connection)
            .await
            .map(SpaceId::from_uuid)
            .map_err(map_sqlx)
    }
}

async fn optional_uuid(
    connection: &mut PoolConnection<Postgres>,
    query: &str,
    id: Uuid,
) -> Result<Option<Uuid>, ApplicationError> {
    sqlx::query_scalar(query)
        .bind(id)
        .fetch_optional(&mut **connection)
        .await
        .map_err(map_sqlx)
}

fn map_sqlx(error: sqlx::Error) -> ApplicationError {
    match &error {
        sqlx::Error::RowNotFound => ApplicationError::NotFound,
        sqlx::Error::Database(database)
            if matches!(
                database.code().as_deref(),
                Some("23503" | "23505" | "23514")
            ) =>
        {
            ApplicationError::Conflict
        }
        sqlx::Error::PoolTimedOut | sqlx::Error::PoolClosed | sqlx::Error::Io(_) => {
            ApplicationError::Unavailable
        }
        _ => ApplicationError::Internal,
    }
}

#[cfg(test)]
mod tests;
#[async_trait]
impl EffectSink for PostgresTransaction {
    async fn queue_agent_suspend(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
        cancel_current_run: bool,
    ) -> Result<(), ApplicationError> {
        self.queue_agent_suspend(agent_id, computer_id, cancel_current_run)
            .await
    }
    async fn queue_agent_configuration(&mut self, agent: &Agent) -> Result<(), ApplicationError> {
        self.queue_agent_configuration(agent).await
    }
    async fn lock_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<(), ApplicationError> {
        self.lock_idempotency(actor, action, key).await
    }
    async fn resource_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<uuid::Uuid>, ApplicationError> {
        self.resource_for_idempotency(actor, action, key).await
    }
    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError> {
        self.save_inbox_item(item).await
    }
    async fn insert_dead_item_notice(
        &mut self,
        agent_id: MemberId,
        thread_id: ThreadId,
        error_code: &'static str,
        now: time::OffsetDateTime,
    ) -> Result<InboxItemId, ApplicationError> {
        self.insert_dead_item_notice(agent_id, thread_id, error_code, now)
            .await
    }
    async fn record_completed_run_event(
        &mut self,
        event_id: EventId,
        run_id: RunId,
    ) -> Result<(), ApplicationError> {
        self.record_completed_run_event(event_id, run_id).await
    }
    async fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError> {
        self.record_task_idempotency(actor, action, key, task_id)
            .await
    }
    async fn record_resource_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        resource_id: uuid::Uuid,
    ) -> Result<(), ApplicationError> {
        self.record_resource_idempotency(actor, action, key, resource_id)
            .await
    }
    async fn record_task_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        task_id: TaskId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.record_task_audit(actor, action, task_id, now).await
    }
    async fn record_inbox_item_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        item_id: InboxItemId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.record_inbox_item_audit(actor, action, item_id, now)
            .await
    }
    fn emit(&mut self, effect: Effect) {
        self.effects.push(effect);
    }
}
