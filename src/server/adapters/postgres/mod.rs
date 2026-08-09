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
        ActionKind, ActionTarget, ActivityEventKind, ActivityEventSnapshot, AgentRetire,
        AttentionNotice, AttentionStrength as WireAttentionStrength, Command, CommandAck,
        CommandDiagnostic, CommandEnvelope, CommandSequence, DeliverySequence, FocusSnapshot,
        InboxItemSnapshot, InboxSourceKind, MessageContent as WireMessageContent, MessageSnapshot,
        NoticeLocation, RunAttachItem, RunNotice, RunStart, RunStop, RunTaskBound,
        SessionChangeReason, SessionCommand, SessionScope, StopReason, TaskSnapshot,
        TaskStatus as WireTaskStatus,
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
    commands: super::command::CommandRegistry,
}

pub(super) struct PostgresTransaction {
    connection: PoolConnection<Postgres>,
    effects: Vec<Effect>,
    notified_computers: std::collections::BTreeSet<Uuid>,
}

#[derive(Clone, Copy)]
struct AmbientActivityEvent {
    channel_id: ChannelId,
    channel_seq: u64,
    kind: ActivityEventKind,
    message_id: Option<MessageId>,
    member_id: Option<MemberId>,
    now: OffsetDateTime,
}

struct AmbientActivityInput {
    space_id: SpaceId,
    member_id: MemberId,
    channel_id: ChannelId,
    thread_id: ThreadId,
    kind: InboxItemKind,
    event: AmbientActivityEvent,
}

struct ChannelMemberActivityInput {
    channel_id: ChannelId,
    actor: MemberId,
    subject_member_id: MemberId,
    thread_id: ThreadId,
    event: AmbientActivityEvent,
}

impl PostgresAdapter {
    pub(super) fn new(pool: PgPool) -> Self {
        Self {
            pool,
            commands: super::command::CommandRegistry::default(),
        }
    }

    pub(super) fn pool(&self) -> PgPool {
        self.pool.clone()
    }

    pub(super) fn commands(&self) -> super::command::CommandRegistry {
        self.commands.clone()
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
                "ALTER TABLE spaces ADD COLUMN accent TEXT NOT NULL DEFAULT '#F0602F'; \
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
        let version: i32 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&mut *transaction)
            .await?;
        if version < 6 {
            if version != 5 {
                return Err(sqlx::Error::Protocol(format!(
                    "unsupported schema baseline version {version}"
                )));
            }
            sqlx::raw_sql(
                "ALTER TABLE channels ADD CONSTRAINT channels_slug_form_check CHECK ( \
                    kind = 'direct' OR ( \
                        char_length(slug) BETWEEN 1 AND 32 \
                        AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' \
                    ) \
                 ); \
                 INSERT INTO schema_meta (version, applied_at) VALUES (6, now());",
            )
            .execute(&mut *transaction)
            .await?;
        }
        let version: i32 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&mut *transaction)
            .await?;
        if version < 7 {
            if version != 6 {
                return Err(sqlx::Error::Protocol(format!(
                    "unsupported schema baseline version {version}"
                )));
            }
            sqlx::raw_sql(
                "ALTER TABLE inbox_items ADD COLUMN ambient_channel_id UUID; \
                 UPDATE inbox_items i SET ambient_channel_id=t.channel_id \
                    FROM messages t WHERE i.thread_id=t.id AND i.kind='channel_activity'; \
                 DROP INDEX IF EXISTS inbox_items_open_ambient_aggregate; \
                 WITH channel_aggregates AS MATERIALIZED ( \
                    SELECT member_id,ambient_channel_id, \
                           (array_agg(id ORDER BY id))[1] AS winner_id, \
                           min(first_message_seq) AS first_message_seq, \
                           max(last_message_seq) AS last_message_seq, \
                           sum(aggregated_count)::integer AS aggregated_count, \
                           min(force_at) AS force_at, \
                           min(available_at) AS available_at \
                    FROM inbox_items \
                    WHERE kind='channel_activity' AND strength='ambient' \
                      AND status='pending' AND retry_count=0 \
                    GROUP BY member_id,ambient_channel_id \
                 ), updated_winners AS ( \
                    UPDATE inbox_items i SET first_message_seq=a.first_message_seq, \
                        last_message_seq=a.last_message_seq,aggregated_count=a.aggregated_count, \
                        force_at=a.force_at,available_at=a.available_at \
                    FROM channel_aggregates a WHERE i.id=a.winner_id \
                    RETURNING i.id \
                 ) \
                 DELETE FROM inbox_items i \
                 USING channel_aggregates a JOIN updated_winners w ON w.id=a.winner_id \
                 WHERE i.member_id=a.member_id AND i.ambient_channel_id=a.ambient_channel_id \
                   AND i.id<>a.winner_id; \
                 CREATE UNIQUE INDEX inbox_items_open_thread_ambient_aggregate \
                    ON inbox_items(member_id, thread_id) \
                    WHERE strength = 'ambient' AND kind = 'thread_activity' AND status = 'pending' AND retry_count = 0; \
                 CREATE UNIQUE INDEX inbox_items_open_channel_ambient_aggregate \
                    ON inbox_items(member_id, ambient_channel_id) \
                    WHERE strength = 'ambient' AND kind = 'channel_activity' AND status = 'pending' AND retry_count = 0; \
                 CREATE TABLE inbox_activity_events ( \
                    inbox_item_id UUID NOT NULL REFERENCES inbox_items(id) ON DELETE RESTRICT, \
                    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE RESTRICT, \
                    channel_seq BIGINT NOT NULL CHECK (channel_seq > 0), \
                    kind TEXT NOT NULL CHECK (kind IN ('message', 'member_joined', 'member_left')), \
                    message_id UUID REFERENCES messages(id) ON DELETE RESTRICT, \
                    member_id UUID REFERENCES members(id) ON DELETE RESTRICT, \
                    created_at TIMESTAMPTZ NOT NULL, \
                    PRIMARY KEY (inbox_item_id, channel_seq), \
                    CHECK ( \
                        (kind = 'message' AND message_id IS NOT NULL AND member_id IS NULL) \
                        OR (kind IN ('member_joined', 'member_left') AND message_id IS NULL AND member_id IS NOT NULL) \
                    ) \
                 ); \
                 INSERT INTO schema_meta (version, applied_at) VALUES (7, now());",
            )
            .execute(&mut *transaction)
            .await?;
        }
        let version: i32 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&mut *transaction)
            .await?;
        if version < 8 {
            if version != 7 {
                return Err(sqlx::Error::Protocol(format!(
                    "unsupported schema baseline version {version}"
                )));
            }
            sqlx::raw_sql(
                "ALTER TABLE agent_runs ADD COLUMN observed_thread_seq BIGINT; \
                 ALTER TABLE member_permissions DROP CONSTRAINT IF EXISTS member_permissions_action_code_check; \
                 ALTER TABLE member_permissions ADD CONSTRAINT member_permissions_action_code_check CHECK (action_code IN ('channel.create', 'channel.invite', 'channel.remove', 'agent.create')); \
                 INSERT INTO schema_meta (version, applied_at) VALUES (8, now());",
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

    /// Settles pending Run commands whose target Run is already terminal. A command can be queued
    /// in the same instant the Run result commits (the attention router and the result writer do
    /// not share one transaction), so replay must not deliver it to the Computer.
    pub(super) async fn settle_stale_run_commands(
        &self,
        computer_id: ComputerId,
        watermark: CommandSequence,
    ) -> Result<(), ApplicationError> {
        let watermark = i64::try_from(watermark.0).map_err(|_| ApplicationError::Conflict)?;
        sqlx::query(
            "UPDATE computer_commands SET acked_at=COALESCE(acked_at,now()) \
             WHERE computer_id=$1 AND computer_seq>$2 AND acked_at IS NULL \
               AND kind IN ('run.start','run.attach_item','run.notice','run.task_bound','run.stop') \
               AND EXISTS ( \
                 SELECT 1 FROM agent_runs r \
                 WHERE r.id=(computer_commands.payload_json #>> '{payload,run_id}')::uuid \
                   AND r.status IN ('completed','yielded','failed','canceled'))",
        )
        .bind(computer_id.into_uuid())
        .bind(watermark)
        .execute(&self.pool)
        .await
        .map_err(map_sqlx)?;
        Ok(())
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

    pub(super) async fn command_is_acknowledged(
        &self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<bool, ApplicationError> {
        let sequence = i64::try_from(sequence).map_err(|_| ApplicationError::Conflict)?;
        sqlx::query_scalar(
            "SELECT acked_at IS NOT NULL FROM computer_commands \
             WHERE id=$1 AND computer_id=$2 AND computer_seq=$3",
        )
        .bind(command_id.into_uuid())
        .bind(computer_id.into_uuid())
        .bind(sequence)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or(ApplicationError::NotFound)
    }

    pub(super) async fn command_diagnostic(
        &self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<Option<CommandDiagnostic>, ApplicationError> {
        let sequence = i64::try_from(sequence).map_err(|_| ApplicationError::Conflict)?;
        let row = sqlx::query(
            "SELECT payload_json FROM computer_commands \
             WHERE id=$1 AND computer_id=$2 AND computer_seq=$3",
        )
        .bind(command_id.into_uuid())
        .bind(computer_id.into_uuid())
        .bind(sequence)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)?;
        let Some(row) = row else {
            return Ok(None);
        };
        let command = serde_json::from_value::<Command>(row.get("payload_json"))
            .map_err(|_| ApplicationError::Internal)?;
        Ok(Some(command.diagnostic()))
    }

    pub(super) async fn run_attach_command_target(
        &self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<Option<(RunId, u64)>, ApplicationError> {
        let sequence = i64::try_from(sequence).map_err(|_| ApplicationError::Conflict)?;
        let row = sqlx::query(
            "SELECT kind,payload_json #>> '{payload,run_id}' AS run_id,\
                    payload_json #>> '{payload,delivery_sequence}' AS delivery_sequence \
             FROM computer_commands WHERE id=$1 AND computer_id=$2 AND computer_seq=$3",
        )
        .bind(command_id.into_uuid())
        .bind(computer_id.into_uuid())
        .bind(sequence)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)?;
        let Some(row) = row else {
            return Err(ApplicationError::NotFound);
        };
        if row.get::<&str, _>("kind") != "run.attach_item" {
            return Ok(None);
        }
        let run_id = Uuid::parse_str(row.get::<&str, _>("run_id"))
            .map_err(|_| ApplicationError::Internal)?;
        let delivery_sequence = row
            .get::<&str, _>("delivery_sequence")
            .parse::<u64>()
            .map_err(|_| ApplicationError::Internal)?;
        Ok(Some((RunId::from_uuid(run_id), delivery_sequence)))
    }

    pub(super) async fn run_start_command_target(
        &self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<Option<RunId>, ApplicationError> {
        let sequence = i64::try_from(sequence).map_err(|_| ApplicationError::Conflict)?;
        let row = sqlx::query(
            "SELECT kind,payload_json #>> '{payload,run_id}' AS run_id \
             FROM computer_commands WHERE id=$1 AND computer_id=$2 AND computer_seq=$3",
        )
        .bind(command_id.into_uuid())
        .bind(computer_id.into_uuid())
        .bind(sequence)
        .fetch_optional(&self.pool)
        .await
        .map_err(map_sqlx)?;
        let Some(row) = row else {
            return Err(ApplicationError::NotFound);
        };
        if row.get::<&str, _>("kind") != "run.start" {
            return Ok(None);
        }
        let run_id = Uuid::parse_str(row.get::<&str, _>("run_id"))
            .map_err(|_| ApplicationError::Internal)?;
        Ok(Some(RunId::from_uuid(run_id)))
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
        let event_id = EventId::from_uuid(Uuid::now_v7());
        if let Err(error) = self
            .record_agent_activity_with_id(event_id, space_id, agent_member_id, kind, payload)
            .await
        {
            tracing::warn!(
                agent_member_id = %agent_member_id.into_uuid(),
                kind,
                error = %error,
                "agent.activity event could not be recorded"
            );
        }
    }

    pub(in crate::server::adapters) async fn record_agent_activity_with_id(
        &self,
        event_id: EventId,
        space_id: SpaceId,
        agent_member_id: MemberId,
        kind: &str,
        payload: serde_json::Value,
    ) -> Result<(), sqlx::Error> {
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
        sqlx::query(
            "INSERT INTO outbox_events (id,space_id,kind,payload_json,created_at) \
             VALUES ($1,$2,'agent.activity',$3,now()) ON CONFLICT (id) DO NOTHING",
        )
        .bind(event_id.into_uuid())
        .bind(space_id.into_uuid())
        .bind(record)
        .execute(&self.pool)
        .await
        .map(|_| ())
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
            notified_computers: std::collections::BTreeSet::new(),
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
                for computer_id in std::mem::take(&mut transaction.notified_computers) {
                    self.commands.notify(computer_id);
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
mod query;
mod rows;
mod task;

pub(in crate::server::adapters) use query::*;
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
    async fn queue_agent_resume(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
    ) -> Result<(), ApplicationError> {
        self.queue_agent_resume(agent_id, computer_id).await
    }
    async fn queue_agent_restart(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
    ) -> Result<(), ApplicationError> {
        self.queue_agent_restart(agent_id, computer_id).await
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
    async fn insert_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError> {
        self.insert_inbox_item(item).await
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
