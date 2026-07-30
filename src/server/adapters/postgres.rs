use std::collections::BTreeSet;

use async_trait::async_trait;
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Postgres, Row, pool::PoolConnection};
use uuid::Uuid;

use crate::{
    ids::{
        AgentId, ChannelId, CommandId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId,
        MessageId, RunId, SpaceId, TaskId, ThreadId,
    },
    protocol::computer::{
        ActionKind, ActionTarget, AgentRetire, AttentionStrength as WireAttentionStrength, Command,
        CommandAck, CommandEnvelope, CommandSequence, DeliverySequence, FencingToken,
        FocusSnapshot, InboxItemSnapshot, InboxSourceKind, MessageContent as WireMessageContent,
        MessageSnapshot, RunAttachItem, RunStart, RunTaskBound, SessionChangeReason,
        SessionCommand, SessionScope, TaskSnapshot, TaskStatus as WireTaskStatus,
    },
    server::{
        application::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort},
        domain::{
            attention::{
                AttentionStrength, InboxItem, InboxItemDisposition, InboxItemKind, InboxItemStatus,
            },
            conversation::{
                Channel, ChannelKind, Message, MessageContent, MessagePlacement, Thread,
            },
            execution::{Run, RunItem, RunOutcome, RunStatus},
            identity::{
                Agent, AgentLifecycle, Computer, ComputerLifecycle, DriverKind, Member,
                PermissionAction,
            },
            task::{CloseReason, RelatedThread, Task, TaskStatus},
        },
    },
};

static MIGRATOR: sqlx::migrate::Migrator = sqlx::migrate!("./migrations/postgres_v2");

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

    pub(super) async fn migrate(&self) -> Result<(), ApplicationError> {
        MIGRATOR
            .run(&self.pool)
            .await
            .map_err(|_| ApplicationError::Unavailable)
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

    pub(super) async fn browser_events(
        &self,
        space_id: SpaceId,
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
        Ok(Some(
            rows.into_iter()
                .map(|row| super::realtime::BrowserEvent {
                    event_id: EventId::from_uuid(row.get("id")),
                    event_type: row.get("kind"),
                    space_id,
                    occurred_at: row.get("created_at"),
                    data: row.get("payload_json"),
                })
                .collect(),
        ))
    }
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

#[async_trait]
impl ServerTransaction for PostgresTransaction {
    async fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError> {
        let row = sqlx::query(
            "SELECT id, space_id, channel_id, root_message_id FROM threads WHERE id = $1 FOR UPDATE",
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
            root_message_id: MessageId::from_uuid(row.get("root_message_id")),
            audience,
        })
    }

    async fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError> {
        let row = sqlx::query(
            "SELECT id, thread_id, author_member_id, placement, content_kind, body_markdown, \
                    action_channel_id, action_agent_member_id, created_at \
             FROM messages WHERE thread_id = $1 AND placement = 'root' FOR UPDATE",
        )
        .bind(thread_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        message_from_row(&row)
    }

    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError> {
        let row = sqlx::query("SELECT * FROM tasks WHERE id = $1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        let link_rows = sqlx::query(
            "SELECT thread_id, linked_by_member_id, linked_at FROM task_threads \
             WHERE task_id = $1 ORDER BY linked_at, thread_id",
        )
        .bind(id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let related_threads = link_rows
            .into_iter()
            .map(|link| RelatedThread {
                thread_id: ThreadId::from_uuid(link.get("thread_id")),
                linked_by_member_id: MemberId::from_uuid(link.get("linked_by_member_id")),
                linked_at: link.get("linked_at"),
            })
            .collect();
        task_from_row(&row, related_threads)
    }

    async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError> {
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
                Ok(RunItem {
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

    async fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError> {
        let row = sqlx::query("SELECT * FROM inbox_items WHERE id = $1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        inbox_from_row(&row)
    }

    async fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError> {
        let row = sqlx::query("SELECT * FROM agents WHERE member_id = $1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(Agent {
            member_id: id,
            space_id: SpaceId::from_uuid(row.get("space_id")),
            computer_id: row
                .get::<Option<Uuid>, _>("computer_id")
                .map(ComputerId::from_uuid),
            role_text: row.get("role_text"),
            role_revision: u64::try_from(row.get::<i64, _>("role_revision"))
                .map_err(|_| ApplicationError::Internal)?,
            lifecycle: agent_lifecycle_from_str(row.get("lifecycle"))?,
            driver_kind: driver_kind_from_str(row.get("driver_kind"))?,
            retired_at: row.get("retired_at"),
        })
    }

    async fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError> {
        let row = sqlx::query(
            "SELECT id, space_id, token_hash, deleted_at FROM computers WHERE id = $1 FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let deleted_at: Option<time::OffsetDateTime> = row.get("deleted_at");
        Ok(Computer {
            id,
            space_id: SpaceId::from_uuid(row.get("space_id")),
            lifecycle: if deleted_at.is_some() {
                ComputerLifecycle::Deleted
            } else {
                ComputerLifecycle::Offline
            },
            token_hash: row.get("token_hash"),
            deleted_at,
        })
    }

    async fn task_for_source(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        optional_uuid(
            &mut self.connection,
            "SELECT id FROM tasks WHERE source_thread_id = $1",
            thread_id.into_uuid(),
        )
        .await
        .map(|value| value.map(TaskId::from_uuid))
    }

    async fn unfinished_task_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        let value = sqlx::query_scalar::<_, Uuid>(
            "SELECT id FROM tasks WHERE status NOT IN ('done', 'closed') AND source_thread_id = $1 \
             UNION ALL \
             SELECT task_id FROM task_threads JOIN tasks ON tasks.id = task_threads.task_id \
             WHERE task_threads.thread_id = $1 AND tasks.status NOT IN ('done', 'closed') LIMIT 1",
        )
        .bind(thread_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(value.map(TaskId::from_uuid))
    }

    async fn task_for_idempotency(
        &mut self,
        actor: MemberId,
        key: IdempotencyKey,
    ) -> Result<Option<TaskId>, ApplicationError> {
        let value = sqlx::query_scalar::<_, Uuid>(
            "SELECT resource_id FROM idempotency_records \
             WHERE actor_member_id = $1 AND action = 'task.create' AND idempotency_key = $2",
        )
        .bind(actor.into_uuid())
        .bind(key.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(value.map(TaskId::from_uuid))
    }

    async fn active_run_for_agent(
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

    async fn computer_has_assigned_agents(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query("SELECT member_id FROM agents WHERE computer_id = $1 FOR UPDATE")
            .bind(computer_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map(|rows| !rows.is_empty())
            .map_err(map_sqlx)
    }

    async fn completed_run_for_event(
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

    async fn can_read_thread(
        &mut self,
        actor: MemberId,
        thread_id: ThreadId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM threads \
             JOIN channel_members ON channel_members.channel_id = threads.channel_id \
             WHERE threads.id = $1 AND channel_members.member_id = $2)",
        )
        .bind(thread_id.into_uuid())
        .bind(actor.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    async fn can_link_thread(
        &mut self,
        actor: MemberId,
        task: &Task,
        target: &Thread,
    ) -> Result<bool, ApplicationError> {
        Ok(self.can_read_thread(actor, task.source_thread_id).await?
            && target.audience.contains(&actor))
    }

    async fn can_assign_agent(
        &mut self,
        agent: MemberId,
        source: &Thread,
    ) -> Result<bool, ApplicationError> {
        let active = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM agents WHERE member_id = $1 \
             AND space_id = $2 AND lifecycle = 'active')",
        )
        .bind(agent.into_uuid())
        .bind(source.space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(active && source.audience.contains(&agent))
    }

    async fn can_govern_task(
        &mut self,
        actor: MemberId,
        task: &Task,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM members WHERE id = $1 AND space_id = $2 \
             AND kind = 'human' AND access_level IN ('owner', 'admin'))",
        )
        .bind(actor.into_uuid())
        .bind(task.space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    async fn has_permission(
        &mut self,
        actor: MemberId,
        action: PermissionAction,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM member_permissions \
             WHERE member_id = $1 AND action_code = $2)",
        )
        .bind(actor.into_uuid())
        .bind(permission_str(action))
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    async fn can_operate_agent(
        &mut self,
        computer_id: ComputerId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM agents JOIN computers ON computers.id=agents.computer_id \
             WHERE agents.member_id=$1 AND agents.computer_id=$2 AND computers.deleted_at IS NULL)",
        )
        .bind(agent_id.into_uuid())
        .bind(computer_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    async fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO tasks (id, space_id, title, status, source_thread_id, creator_member_id, \
             assignee_agent_member_id, result_message_id, close_reason_code, close_reason_note, \
             created_at, updated_at, finished_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)",
        )
        .bind(task.id.into_uuid())
        .bind(task.space_id.into_uuid())
        .bind(&task.title)
        .bind(task_status_str(task.status))
        .bind(task.source_thread_id.into_uuid())
        .bind(task.creator_member_id.into_uuid())
        .bind(task.assignee_agent_member_id.map(MemberId::into_uuid))
        .bind(task.result_message_id.map(MessageId::into_uuid))
        .bind(task.close_reason.map(close_reason_str))
        .bind(&task.close_reason_note)
        .bind(task.created_at)
        .bind(task.updated_at)
        .bind(task.finished_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        self.replace_task_links(&task).await
    }

    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE tasks SET title=$2,status=$3,assignee_agent_member_id=$4,result_message_id=$5, \
             close_reason_code=$6,close_reason_note=$7,updated_at=$8,finished_at=$9 \
             WHERE id=$1 AND source_thread_id=$10",
        )
        .bind(task.id.into_uuid())
        .bind(&task.title)
        .bind(task_status_str(task.status))
        .bind(task.assignee_agent_member_id.map(MemberId::into_uuid))
        .bind(task.result_message_id.map(MessageId::into_uuid))
        .bind(task.close_reason.map(close_reason_str))
        .bind(&task.close_reason_note)
        .bind(task.updated_at)
        .bind(task.finished_at)
        .bind(task.source_thread_id.into_uuid())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() != 1 {
            return Err(ApplicationError::NotFound);
        }
        self.replace_task_links(&task).await
    }

    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO agent_runs (id,space_id,agent_id,task_id,focus_thread_id,status, \
             fencing_token_hash,lease_expires_at,outcome_code,continuation_note,created_at,started_at,finished_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now(),$11,$12) \
             ON CONFLICT (id) DO UPDATE SET task_id=EXCLUDED.task_id,status=EXCLUDED.status, \
             lease_expires_at=EXCLUDED.lease_expires_at,outcome_code=EXCLUDED.outcome_code, \
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

    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE inbox_items SET task_id=$2,status=$3,available_at=$4,lease_run_id=$5, \
             lease_expires_at=$6,retry_count=$7,handled_at=$8 WHERE id=$1",
        )
        .bind(item.id.into_uuid())
        .bind(item.task_id.map(TaskId::into_uuid))
        .bind(inbox_status_str(item.status))
        .bind(item.available_at)
        .bind(item.lease_run_id.map(RunId::into_uuid))
        .bind(item.lease_expires_at)
        .bind(i32::try_from(item.retry_count).map_err(|_| ApplicationError::Conflict)?)
        .bind(item.handled_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }

    async fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError> {
        let location = sqlx::query("SELECT channel_id,space_id FROM threads WHERE id=$1")
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

    async fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
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

    async fn insert_agent(&mut self, member: Member, agent: Agent) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) \
             VALUES ($1,$2,'agent',$3,$4,'member',$5)",
        )
        .bind(member.id.into_uuid())
        .bind(member.space_id.into_uuid())
        .bind(&member.display_name)
        .bind(&member.handle)
        .bind(member.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle, \
             driver_kind,driver_config_json,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'{}',$8)",
        )
        .bind(agent.member_id.into_uuid())
        .bind(agent.space_id.into_uuid())
        .bind(agent.computer_id.map(ComputerId::into_uuid))
        .bind(&agent.role_text)
        .bind(i64::try_from(agent.role_revision).map_err(|_| ApplicationError::Conflict)?)
        .bind(agent_lifecycle_str(agent.lifecycle))
        .bind(driver_kind_str(agent.driver_kind))
        .bind(member.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE agents SET computer_id=$2,role_text=$3,role_revision=$4,lifecycle=$5,retired_at=$6 \
             WHERE member_id=$1",
        )
        .bind(agent.member_id.into_uuid())
        .bind(agent.computer_id.map(ComputerId::into_uuid))
        .bind(&agent.role_text)
        .bind(i64::try_from(agent.role_revision).map_err(|_| ApplicationError::Conflict)?)
        .bind(agent_lifecycle_str(agent.lifecycle))
        .bind(agent.retired_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }

    async fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE computers SET token_hash=$2,deleted_at=$3,connection_status='offline' WHERE id=$1",
        )
        .bind(computer.id.into_uuid())
        .bind(&computer.token_hash)
        .bind(computer.deleted_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if changed.rows_affected() == 1 {
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }

    async fn record_completed_run_event(
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

    async fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError> {
        let hash = Sha256::digest(task_id.into_uuid().as_bytes());
        sqlx::query(
            "INSERT INTO idempotency_records (actor_member_id,action,idempotency_key,response_code, \
             resource_id,result_hash,created_at) VALUES ($1,'task.create',$2,'created',$3,$4,now())",
        )
        .bind(actor.into_uuid())
        .bind(key.into_uuid())
        .bind(task_id.into_uuid())
        .bind(hash.as_slice())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    fn emit(&mut self, effect: Effect) {
        self.effects.push(effect);
    }
}

impl PostgresTransaction {
    async fn rollback(&mut self) {
        let _ = sqlx::query("ROLLBACK").execute(&mut *self.connection).await;
    }

    async fn thread_audience(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<BTreeSet<MemberId>, ApplicationError> {
        sqlx::query_scalar::<_, Uuid>(
            "SELECT channel_members.member_id FROM threads JOIN channel_members \
             ON channel_members.channel_id=threads.channel_id WHERE threads.id=$1 ORDER BY member_id",
        )
        .bind(thread_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map(|ids| ids.into_iter().map(MemberId::from_uuid).collect())
        .map_err(map_sqlx)
    }

    async fn replace_task_links(&mut self, task: &Task) -> Result<(), ApplicationError> {
        sqlx::query("DELETE FROM task_threads WHERE task_id=$1")
            .bind(task.id.into_uuid())
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        for link in &task.related_threads {
            sqlx::query(
                "INSERT INTO task_threads (task_id,thread_id,space_id,linked_by_member_id,linked_at) \
                 VALUES ($1,$2,$3,$4,$5)",
            )
            .bind(task.id.into_uuid())
            .bind(link.thread_id.into_uuid())
            .bind(task.space_id.into_uuid())
            .bind(link.linked_by_member_id.into_uuid())
            .bind(link.linked_at)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        Ok(())
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
            Effect::RunClaimed {
                run_id,
                fencing_token,
            } => {
                let computer_id = self.computer_for_run(run_id).await?;
                let command = self.run_start(run_id, fencing_token.expose()).await?;
                self.queue_command(computer_id, Command::RunStart(command))
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
            Effect::AgentCreated {
                agent_id,
                computer_id,
            } => {
                let configuration = self.agent_configuration(agent_id).await?;
                self.queue_command(computer_id, Command::AgentProvision(configuration))
                    .await?;
                ("agent.created", agent_id.into_uuid())
            }
        };
        let space_id = self.space_for_subject(kind, subject_id).await?;
        Ok((space_id, kind, json!({"resource_id": subject_id})))
    }

    async fn queue_command(
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

    async fn agent_configuration(
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

    async fn computer_for_run(&mut self, run_id: RunId) -> Result<ComputerId, ApplicationError> {
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

    async fn task_assignment(
        &mut self,
        task_id: TaskId,
    ) -> Result<Option<(MemberId, ComputerId)>, ApplicationError> {
        let row = sqlx::query(
            "SELECT agents.member_id,agents.computer_id FROM tasks \
             JOIN agents ON agents.member_id=tasks.assignee_agent_member_id WHERE tasks.id=$1",
        )
        .bind(task_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| {
            (
                MemberId::from_uuid(row.get("member_id")),
                ComputerId::from_uuid(row.get("computer_id")),
            )
        }))
    }

    async fn run_start(
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

    async fn task_snapshot(&mut self, task_id: TaskId) -> Result<TaskSnapshot, ApplicationError> {
        let row = sqlx::query(
            "SELECT title,status,source_thread_id,result_message_id FROM tasks WHERE id=$1",
        )
        .bind(task_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let source_thread_id = ThreadId::from_uuid(row.get("source_thread_id"));
        let mut linked_thread_ids = vec![source_thread_id];
        linked_thread_ids.extend(
            sqlx::query_scalar::<_, Uuid>(
                "SELECT thread_id FROM task_threads WHERE task_id=$1 ORDER BY linked_at,thread_id",
            )
            .bind(task_id.into_uuid())
            .fetch_all(&mut *self.connection)
            .await
            .map_err(map_sqlx)?
            .into_iter()
            .map(ThreadId::from_uuid),
        );
        Ok(TaskSnapshot {
            task_id,
            title: row.get("title"),
            status: wire_task_status(row.get("status"))?,
            source_thread_id,
            linked_thread_ids,
            result_message_id: row
                .get::<Option<Uuid>, _>("result_message_id")
                .map(MessageId::from_uuid),
        })
    }

    async fn focus_snapshot(
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

    async fn inbox_snapshot(
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
        let query = if kind.starts_with("task.") || kind == "session.close" {
            "SELECT space_id FROM tasks WHERE id=$1"
        } else if kind.starts_with("run.") {
            "SELECT space_id FROM agent_runs WHERE id=$1"
        } else if kind.starts_with("agent.") {
            "SELECT space_id FROM agents WHERE member_id=$1"
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

fn message_from_row(row: &sqlx::postgres::PgRow) -> Result<Message, ApplicationError> {
    let content = match row.get::<&str, _>("content_kind") {
        "text" => MessageContent::Text(row.get("body_markdown")),
        "channel_created" => {
            MessageContent::ChannelCreated(ChannelId::from_uuid(row.get("action_channel_id")))
        }
        "agent_created" => {
            MessageContent::AgentCreated(MemberId::from_uuid(row.get("action_agent_member_id")))
        }
        _ => return Err(ApplicationError::Internal),
    };
    Ok(Message {
        id: MessageId::from_uuid(row.get("id")),
        thread_id: ThreadId::from_uuid(row.get("thread_id")),
        author_member_id: MemberId::from_uuid(row.get("author_member_id")),
        placement: placement_from_str(row.get("placement"))?,
        content,
        created_at: row.get("created_at"),
    })
}

fn task_from_row(
    row: &sqlx::postgres::PgRow,
    related_threads: Vec<RelatedThread>,
) -> Result<Task, ApplicationError> {
    Ok(Task {
        id: TaskId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        title: row.get("title"),
        status: task_status_from_str(row.get("status"))?,
        source_thread_id: ThreadId::from_uuid(row.get("source_thread_id")),
        creator_member_id: MemberId::from_uuid(row.get("creator_member_id")),
        assignee_agent_member_id: row
            .get::<Option<Uuid>, _>("assignee_agent_member_id")
            .map(MemberId::from_uuid),
        result_message_id: row
            .get::<Option<Uuid>, _>("result_message_id")
            .map(MessageId::from_uuid),
        close_reason: row
            .get::<Option<String>, _>("close_reason_code")
            .map(|value| close_reason_from_str(&value))
            .transpose()?,
        close_reason_note: row.get("close_reason_note"),
        related_threads,
        created_at: row.get("created_at"),
        updated_at: row.get("updated_at"),
        finished_at: row.get("finished_at"),
    })
}

fn run_from_row(row: &sqlx::postgres::PgRow, items: Vec<RunItem>) -> Result<Run, ApplicationError> {
    Ok(Run {
        id: RunId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        agent_id: MemberId::from_uuid(row.get("agent_id")),
        task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
        focus_thread_id: ThreadId::from_uuid(row.get("focus_thread_id")),
        status: run_status_from_str(row.get("status"))?,
        fencing_token_hash: row.get("fencing_token_hash"),
        lease_expires_at: row.get("lease_expires_at"),
        items,
        outcome: row
            .get::<Option<String>, _>("outcome_code")
            .map(|value| run_outcome_from_str(&value))
            .transpose()?,
        continuation_note: row.get("continuation_note"),
        started_at: row.get("started_at"),
        finished_at: row.get("finished_at"),
    })
}

fn inbox_from_row(row: &sqlx::postgres::PgRow) -> Result<InboxItem, ApplicationError> {
    Ok(InboxItem {
        id: InboxItemId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        agent_id: MemberId::from_uuid(row.get("agent_id")),
        message_id: row
            .get::<Option<Uuid>, _>("message_id")
            .map(MessageId::from_uuid),
        thread_id: ThreadId::from_uuid(row.get("thread_id")),
        task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
        kind: inbox_kind_from_str(row.get("kind"))?,
        strength: strength_from_str(row.get("strength"))?,
        status: inbox_status_from_str(row.get("status"))?,
        available_at: row.get("available_at"),
        lease_run_id: row
            .get::<Option<Uuid>, _>("lease_run_id")
            .map(RunId::from_uuid),
        lease_expires_at: row.get("lease_expires_at"),
        retry_count: u32::try_from(row.get::<i32, _>("retry_count"))
            .map_err(|_| ApplicationError::Internal)?,
        handled_at: row.get("handled_at"),
    })
}

fn command_kind(command: &Command) -> &'static str {
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

fn wire_message(row: &sqlx::postgres::PgRow) -> Result<MessageSnapshot, ApplicationError> {
    let content = match row.get::<&str, _>("content_kind") {
        "text" => WireMessageContent::Text {
            markdown: row.get("body_markdown"),
        },
        "channel_created" => WireMessageContent::Action {
            action: ActionKind::ChannelCreated,
            target: ActionTarget::Channel(ChannelId::from_uuid(row.get("action_channel_id"))),
        },
        "agent_created" => WireMessageContent::Action {
            action: ActionKind::AgentCreated,
            target: ActionTarget::Agent(AgentId::from_uuid(row.get("action_agent_member_id"))),
        },
        _ => return Err(ApplicationError::Internal),
    };
    Ok(MessageSnapshot {
        message_id: MessageId::from_uuid(row.get("id")),
        author_member_id: MemberId::from_uuid(row.get("author_member_id")),
        sequence: u64::try_from(row.get::<i64, _>("channel_seq"))
            .map_err(|_| ApplicationError::Internal)?,
        content,
        created_at: row.get("created_at"),
    })
}

fn wire_task_status(value: &str) -> Result<WireTaskStatus, ApplicationError> {
    match value {
        "todo" => Ok(WireTaskStatus::Todo),
        "in_progress" => Ok(WireTaskStatus::InProgress),
        "in_review" => Ok(WireTaskStatus::InReview),
        "done" => Ok(WireTaskStatus::Done),
        "closed" => Ok(WireTaskStatus::Closed),
        _ => Err(ApplicationError::Internal),
    }
}

fn wire_inbox_kind(value: &str) -> Result<InboxSourceKind, ApplicationError> {
    match value {
        "direct" => Ok(InboxSourceKind::Direct),
        "mention" => Ok(InboxSourceKind::Mention),
        "reply" => Ok(InboxSourceKind::Reply),
        "task_activity" => Ok(InboxSourceKind::TaskActivity),
        "thread_activity" => Ok(InboxSourceKind::ThreadActivity),
        "channel_activity" => Ok(InboxSourceKind::ChannelActivity),
        "system" => Ok(InboxSourceKind::System),
        _ => Err(ApplicationError::Internal),
    }
}

fn wire_strength(value: &str) -> Result<WireAttentionStrength, ApplicationError> {
    match value {
        "hard" => Ok(WireAttentionStrength::Hard),
        "ambient" => Ok(WireAttentionStrength::Ambient),
        _ => Err(ApplicationError::Internal),
    }
}

macro_rules! text_enum {
    ($to:ident, $from:ident, $ty:ty, {$($variant:path => $text:literal),+ $(,)?}) => {
        fn $to(value: $ty) -> &'static str { match value { $($variant => $text),+ } }
        fn $from(value: &str) -> Result<$ty, ApplicationError> { match value { $($text => Ok($variant)),+, _ => Err(ApplicationError::Internal) } }
    };
}

text_enum!(task_status_str, task_status_from_str, TaskStatus, {
    TaskStatus::Todo => "todo", TaskStatus::InProgress => "in_progress", TaskStatus::InReview => "in_review", TaskStatus::Done => "done", TaskStatus::Closed => "closed"
});
text_enum!(close_reason_str, close_reason_from_str, CloseReason, {
    CloseReason::Invalid => "invalid", CloseReason::Duplicate => "duplicate", CloseReason::NotNeeded => "not_needed", CloseReason::Obsolete => "obsolete", CloseReason::Other => "other"
});
text_enum!(run_status_str, run_status_from_str, RunStatus, {
    RunStatus::Queued => "queued", RunStatus::Starting => "starting", RunStatus::Running => "running", RunStatus::Finalizing => "finalizing", RunStatus::Completed => "completed", RunStatus::Yielded => "yielded", RunStatus::Failed => "failed", RunStatus::Stopping => "stopping", RunStatus::Canceled => "canceled"
});
text_enum!(run_outcome_str, run_outcome_from_str, RunOutcome, {
    RunOutcome::Completed => "completed", RunOutcome::Yielded => "yielded", RunOutcome::Failed => "failed", RunOutcome::Canceled => "canceled"
});
text_enum!(disposition_str, disposition_from_str, InboxItemDisposition, {
    InboxItemDisposition::Handled => "handled", InboxItemDisposition::Deferred => "deferred", InboxItemDisposition::Released => "released"
});
text_enum!(inbox_status_str, inbox_status_from_str, InboxItemStatus, {
    InboxItemStatus::Pending => "pending", InboxItemStatus::Leased => "leased", InboxItemStatus::Deferred => "deferred", InboxItemStatus::Handled => "handled", InboxItemStatus::Dead => "dead"
});
text_enum!(inbox_kind_str, inbox_kind_from_str, InboxItemKind, {
    InboxItemKind::Direct => "direct", InboxItemKind::Mention => "mention", InboxItemKind::Reply => "reply", InboxItemKind::TaskActivity => "task_activity", InboxItemKind::ThreadActivity => "thread_activity", InboxItemKind::ChannelActivity => "channel_activity", InboxItemKind::System => "system"
});
text_enum!(strength_str, strength_from_str, AttentionStrength, {
    AttentionStrength::Hard => "hard", AttentionStrength::Ambient => "ambient"
});
text_enum!(placement_str, placement_from_str, MessagePlacement, {
    MessagePlacement::Root => "root", MessagePlacement::Reply => "reply"
});
text_enum!(channel_kind_str, channel_kind_from_str, ChannelKind, {
    ChannelKind::Public => "public", ChannelKind::Private => "private", ChannelKind::Direct => "direct"
});
text_enum!(agent_lifecycle_str, agent_lifecycle_from_str, AgentLifecycle, {
    AgentLifecycle::Provisioning => "provisioning", AgentLifecycle::Active => "active", AgentLifecycle::Suspended => "suspended", AgentLifecycle::Retired => "retired", AgentLifecycle::Error => "error"
});
text_enum!(driver_kind_str, driver_kind_from_str, DriverKind, {
    DriverKind::Codex => "codex", DriverKind::Builtin => "builtin"
});

fn permission_str(value: PermissionAction) -> &'static str {
    match value {
        PermissionAction::ChannelCreate => "channel.create",
        PermissionAction::AgentCreate => "agent.create",
    }
}

#[cfg(test)]
mod tests {
    use std::str::FromStr;

    use sqlx::{Connection, PgConnection, postgres::PgConnectOptions};
    use time::OffsetDateTime;
    use url::Url;

    use super::*;
    use crate::server::application::conversation::{CreateAgentAction, CreateAgentActionInput};
    use crate::server::application::task::{
        CreateTaskFromRootMessage, CreateTaskInput, TaskSource,
    };

    #[tokio::test]
    async fn empty_database_builds_final_schema_with_concurrency_constraints() {
        let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
            .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
        let database_name = format!("sumi_server_adapter_{}", Uuid::now_v7().simple());
        let mut admin =
            PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
                .await
                .unwrap();
        sqlx::query(&format!("CREATE DATABASE \"{database_name}\""))
            .execute(&mut admin)
            .await
            .unwrap();

        let mut database_url = Url::parse(&admin_url).unwrap();
        database_url.set_path(&format!("/{database_name}"));
        let result = async {
            let pool = PgPool::connect(database_url.as_str()).await.unwrap();
            PostgresAdapter::new(pool.clone()).migrate().await.unwrap();

            let active_index: bool = sqlx::query_scalar(
                "SELECT EXISTS(SELECT 1 FROM pg_indexes \
                 WHERE indexname='agent_runs_one_active_per_agent' \
                 AND indexdef LIKE '%WHERE (status <> ALL%')",
            )
            .fetch_one(&pool)
            .await
            .unwrap();
            let deferred_thread_cycle: bool = sqlx::query_scalar(
                "SELECT condeferrable AND condeferred FROM pg_constraint \
                 WHERE conname='messages_thread_in_space'",
            )
            .fetch_one(&pool)
            .await
            .unwrap();
            let legacy_session_table: bool = sqlx::query_scalar(
                "SELECT EXISTS(SELECT 1 FROM information_schema.tables \
                 WHERE table_schema='public' AND table_name='sessions')",
            )
            .fetch_one(&pool)
            .await
            .unwrap();

            assert!(active_index);
            assert!(deferred_thread_cycle);
            assert!(!legacy_session_table);
            pool.close().await;
        }
        .await;

        sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
            .execute(&mut admin)
            .await
            .unwrap();
        result
    }

    #[tokio::test]
    async fn application_transaction_commits_task_source_idempotency_and_outbox_together() {
        let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
            .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
        let database_name = format!("sumi_postgres_port_{}", Uuid::now_v7().simple());
        let mut admin =
            PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
                .await
                .unwrap();
        sqlx::query(&format!("CREATE DATABASE \"{database_name}\""))
            .execute(&mut admin)
            .await
            .unwrap();
        let mut database_url = Url::parse(&admin_url).unwrap();
        database_url.set_path(&format!("/{database_name}"));

        let pool = PgPool::connect(database_url.as_str()).await.unwrap();
        let mut adapter = PostgresAdapter::new(pool.clone());
        adapter.migrate().await.unwrap();
        let space = Uuid::now_v7();
        let member = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let root = Uuid::now_v7();
        let actor_agent = Uuid::now_v7();
        let computer_id = Uuid::now_v7();
        let run_id = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces (id,slug,name,owner_member_id,created_at) VALUES ('{space}','space','Space','{member}',now());
             INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{member}','{space}','human','Owner','owner','owner',now());
             INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{actor_agent}','{space}','agent','Actor','actor','member',now());
             INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','offline',1,now());
             INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{actor_agent}','{space}','{computer_id}','Act',1,'active','codex',now());
             INSERT INTO member_permissions (member_id,space_id,action_code,granted_by_member_id,created_at) VALUES ('{actor_agent}','{space}','agent.create','{member}',now());
             INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','private','general',2,now());
             INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{member}',now(),0);
             INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{actor_agent}',now(),0);
             INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{member}','source',now());
             INSERT INTO threads (id,space_id,channel_id,root_message_id,created_at) VALUES ('{root}','{space}','{channel}','{root}',now());
             INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,fencing_token_hash,lease_expires_at,created_at,started_at) VALUES ('{run_id}','{space}','{actor_agent}','{root}','running','hash',now()+interval '1 hour',now(),now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

        let task_id = TaskId::from_uuid(Uuid::now_v7());
        let idempotency_key = IdempotencyKey::from_uuid(Uuid::now_v7());
        let created = CreateTaskFromRootMessage::execute(
            &mut adapter,
            CreateTaskInput {
                task_id,
                actor_member_id: MemberId::from_uuid(member),
                source: TaskSource::HumanRoot(ThreadId::from_uuid(root)),
                title: "PostgreSQL transaction".into(),
                assignee_agent_member_id: None,
                idempotency_key,
                now: OffsetDateTime::now_utc(),
            },
        )
        .await
        .unwrap();
        assert_eq!(created.id, task_id);
        let facts: (i64, i64, i64) = (
            sqlx::query_scalar("SELECT count(*) FROM tasks WHERE id=$1")
                .bind(task_id.into_uuid())
                .fetch_one(&pool)
                .await
                .unwrap(),
            sqlx::query_scalar("SELECT count(*) FROM idempotency_records WHERE resource_id=$1")
                .bind(task_id.into_uuid())
                .fetch_one(&pool)
                .await
                .unwrap(),
            sqlx::query_scalar("SELECT count(*) FROM outbox_events WHERE kind='task.created'")
                .fetch_one(&pool)
                .await
                .unwrap(),
        );
        assert_eq!(facts, (1, 1, 1));
        let invalid_done =
            sqlx::query("UPDATE tasks SET status='done',finished_at=now() WHERE id=$1")
                .bind(task_id.into_uuid())
                .execute(&pool)
                .await
                .unwrap_err();
        assert_eq!(
            invalid_done.as_database_error().unwrap().code().as_deref(),
            Some("23514")
        );
        let parallel_run = sqlx::query(
            "INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status, \
             fencing_token_hash,lease_expires_at,created_at) \
             VALUES ($1,$2,$3,$4,'queued','other',now()+interval '1 hour',now())",
        )
        .bind(Uuid::now_v7())
        .bind(space)
        .bind(actor_agent)
        .bind(root)
        .execute(&pool)
        .await
        .unwrap_err();
        assert_eq!(
            parallel_run.as_database_error().unwrap().code().as_deref(),
            Some("23505")
        );

        let created_agent = MemberId::from_uuid(Uuid::now_v7());
        CreateAgentAction::execute(
            &mut adapter,
            CreateAgentActionInput {
                agent_member_id: created_agent,
                display_name: "New Agent".into(),
                handle: "new-agent".into(),
                role_text: "Implement".into(),
                computer_id: ComputerId::from_uuid(computer_id),
                driver_kind: DriverKind::Codex,
                action_message_id: MessageId::from_uuid(Uuid::now_v7()),
                actor_member_id: MemberId::from_uuid(actor_agent),
                current_run_id: RunId::from_uuid(run_id),
                now: OffsetDateTime::now_utc(),
            },
        )
        .await
        .unwrap();
        let agent_action_facts: (i64, i64, i64) = (
            sqlx::query_scalar("SELECT count(*) FROM agents WHERE member_id=$1")
                .bind(created_agent.into_uuid())
                .fetch_one(&pool)
                .await
                .unwrap(),
            sqlx::query_scalar(
                "SELECT count(*) FROM messages WHERE content_kind='agent_created' \
                 AND action_agent_member_id=$1",
            )
            .bind(created_agent.into_uuid())
            .fetch_one(&pool)
            .await
            .unwrap(),
            sqlx::query_scalar(
                "SELECT count(*) FROM computer_commands WHERE kind='agent.provision'",
            )
            .fetch_one(&pool)
            .await
            .unwrap(),
        );
        assert_eq!(agent_action_facts, (1, 1, 1));
        let commands = adapter
            .pending_commands(ComputerId::from_uuid(computer_id), CommandSequence(0))
            .await
            .unwrap();
        assert_eq!(commands.len(), 1);
        assert!(matches!(commands[0].command, Command::AgentProvision(_)));
        adapter
            .acknowledge_command(
                ComputerId::from_uuid(computer_id),
                &CommandAck {
                    command_id: commands[0].command_id,
                    sequence: commands[0].sequence,
                },
            )
            .await
            .unwrap();
        assert!(
            adapter
                .pending_commands(ComputerId::from_uuid(computer_id), CommandSequence(0))
                .await
                .unwrap()
                .is_empty()
        );

        pool.close().await;
        sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
            .execute(&mut admin)
            .await
            .unwrap();
    }
}
