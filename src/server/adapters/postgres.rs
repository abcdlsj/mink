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
        CommandSequence, DeliverySequence, FencingToken, FocusSnapshot, InboxItemSnapshot,
        InboxSourceKind, MessageContent as WireMessageContent, MessageSnapshot, NoticeLocation,
        RunAttachItem, RunNotice, RunStart, RunTaskBound, SessionChangeReason, SessionCommand,
        SessionScope, TaskSnapshot, TaskStatus as WireTaskStatus,
    },
    server::{
        application::ports::{
            ApplicationError, AuthenticatedHuman, ComputerRecord, CreatedSpace, Effect,
            MessageDraft, PairedComputer, PublishedMessage, ServerTransaction, TransactionPort,
        },
        domain::{
            access::{HumanRegistration, SpaceAccess},
            attachment::{Attachment, AttachmentStatus},
            attention::{
                AttentionStrength, InboxItem, InboxItemDisposition, InboxItemKind, InboxItemStatus,
            },
            conversation::{
                Channel, ChannelKind, Message, MessageContent, MessagePlacement, Thread,
            },
            execution::{Run, RunErrorCode, RunItem, RunOutcome, RunStatus},
            identity::{
                AccessLevel, Agent, AgentLifecycle, Computer, ComputerLifecycle, DriverKind,
                Member, PermissionAction,
            },
            pairing::{ComputerOs, Pairing, PairingRequest, PairingStatus},
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

    pub(super) async fn migrate(&self) -> Result<(), sqlx::migrate::MigrateError> {
        MIGRATOR.run(&self.pool).await
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
    async fn create_space(
        &mut self,
        actor_user_id: Uuid,
        space_id: SpaceId,
        owner_id: MemberId,
        general_channel_id: ChannelId,
        name: &str,
        slug: &str,
        owner_handle: &str,
        owner_display_name: &str,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<CreatedSpace, ApplicationError> {
        let lock_key = format!(
            "{}:space.create:{}",
            actor_user_id,
            idempotency_key.into_uuid()
        );
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(lock_key)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        if let Some(row) = sqlx::query(
            "SELECT s.id,s.owner_member_id,(SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
             FROM idempotency_records records \
             JOIN human_members hm ON hm.member_id=records.actor_member_id \
             JOIN spaces s ON s.id=records.resource_id \
             WHERE hm.user_id=$1 AND records.action='space.create' AND records.idempotency_key=$2",
        )
        .bind(actor_user_id)
        .bind(idempotency_key.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?
        {
            return Ok(CreatedSpace {
                space_id: SpaceId::from_uuid(row.get("id")),
                owner_id: MemberId::from_uuid(row.get("owner_member_id")),
                general_channel_id: ChannelId::from_uuid(row.get("general_channel_id")),
            });
        }
        sqlx::query("SET CONSTRAINTS ALL DEFERRED")
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO spaces(id,slug,name,owner_member_id,created_at) VALUES($1,$2,$3,$4,$5)",
        )
        .bind(space_id.into_uuid())
        .bind(slug)
        .bind(name)
        .bind(owner_id.into_uuid())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES($1,$2,'human',$3,$4,'owner',$5)")
            .bind(owner_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(owner_display_name)
            .bind(owner_handle)
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO human_members(member_id,space_id,user_id) VALUES($1,$2,$3)")
            .bind(owner_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(actor_user_id)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO channels(id,space_id,kind,slug,topic,created_at) VALUES($1,$2,'public','general',NULL,$3)")
            .bind(general_channel_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO channel_members(channel_id,space_id,member_id,joined_at) VALUES($1,$2,$3,$4)")
            .bind(general_channel_id.into_uuid())
            .bind(space_id.into_uuid())
            .bind(owner_id.into_uuid())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        let result_hash = Sha256::digest(space_id.into_uuid().as_bytes());
        sqlx::query("INSERT INTO idempotency_records(actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) VALUES($1,'space.create',$2,'ok',$3,$4,$5)")
            .bind(owner_id.into_uuid())
            .bind(idempotency_key.into_uuid())
            .bind(space_id.into_uuid())
            .bind(result_hash.as_slice())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,created_at) VALUES($1,$2,$3,'space.created','space',$2,$4)")
            .bind(Uuid::now_v7())
            .bind(space_id.into_uuid())
            .bind(owner_id.into_uuid())
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,'channel.created',$3,$4)")
            .bind(Uuid::now_v7())
            .bind(space_id.into_uuid())
            .bind(json!({"resource_id": general_channel_id.into_uuid()}))
            .bind(now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(CreatedSpace {
            space_id,
            owner_id,
            general_channel_id,
        })
    }

    async fn insert_human(
        &mut self,
        user_id: Uuid,
        registration: &HumanRegistration,
        password_hash: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO users(id,email_normalized,password_hash,display_name,created_at) \
             VALUES($1,$2,$3,$4,$5)",
        )
        .bind(user_id)
        .bind(&registration.email_normalized)
        .bind(password_hash)
        .bind(&registration.display_name)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn human_credential(
        &mut self,
        email_normalized: &str,
    ) -> Result<Option<(AuthenticatedHuman, String)>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,display_name,email_normalized,password_hash FROM users \
             WHERE email_normalized=$1 AND disabled_at IS NULL",
        )
        .bind(email_normalized)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| {
            (
                AuthenticatedHuman {
                    user_id: row.get("id"),
                    display_name: row.get("display_name"),
                    email_normalized: row.get("email_normalized"),
                },
                row.get("password_hash"),
            )
        }))
    }

    async fn insert_browser_session(
        &mut self,
        session_id: Uuid,
        user_id: Uuid,
        token_hash: &str,
        expires_at: OffsetDateTime,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO browser_sessions(id,user_id,token_hash,expires_at,last_seen_at,created_at) \
             VALUES($1,$2,$3,$4,$5,$5)",
        )
        .bind(session_id)
        .bind(user_id)
        .bind(token_hash)
        .bind(expires_at)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn human_for_session(
        &mut self,
        token_hash: &str,
        now: OffsetDateTime,
    ) -> Result<Option<AuthenticatedHuman>, ApplicationError> {
        let row = sqlx::query(
            "SELECT u.id,u.display_name,u.email_normalized \
             FROM browser_sessions s JOIN users u ON u.id=s.user_id \
             WHERE s.token_hash=$1 AND s.expires_at>$2 AND u.disabled_at IS NULL",
        )
        .bind(token_hash)
        .bind(now)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(row.map(|row| AuthenticatedHuman {
            user_id: row.get("id"),
            display_name: row.get("display_name"),
            email_normalized: row.get("email_normalized"),
        }))
    }

    async fn delete_browser_session(&mut self, token_hash: &str) -> Result<(), ApplicationError> {
        sqlx::query("DELETE FROM browser_sessions WHERE token_hash=$1")
            .bind(token_hash)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    async fn space_access(
        &mut self,
        user_id: Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceAccess>, ApplicationError> {
        let row = sqlx::query(
            "SELECT members.id,members.access_level FROM human_members \
             JOIN members ON members.id=human_members.member_id \
             AND members.space_id=human_members.space_id \
             WHERE human_members.space_id=$1 AND human_members.user_id=$2",
        )
        .bind(space_id.into_uuid())
        .bind(user_id)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        row.map(|row| {
            Ok(SpaceAccess {
                member_id: MemberId::from_uuid(row.get("id")),
                space_id,
                access_level: access_level_from_str(row.get("access_level"))?,
            })
        })
        .transpose()
    }

    async fn channel_access(
        &mut self,
        user_id: Uuid,
        channel_id: ChannelId,
    ) -> Result<Option<MemberId>, ApplicationError> {
        let member_id = sqlx::query_scalar::<_, Uuid>(
            "SELECT human_members.member_id FROM channels \
             JOIN human_members ON human_members.space_id=channels.space_id \
             JOIN channel_members ON channel_members.channel_id=channels.id \
             AND channel_members.member_id=human_members.member_id \
             WHERE channels.id=$1 AND human_members.user_id=$2",
        )
        .bind(channel_id.into_uuid())
        .bind(user_id)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(member_id.map(MemberId::from_uuid))
    }

    async fn space_of_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        let space_id =
            sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM agents WHERE member_id=$1")
                .bind(agent_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        Ok(space_id.map(SpaceId::from_uuid))
    }

    async fn space_of_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM computers WHERE id=$1")
            .bind(computer_id.into_uuid())
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(space_id.map(SpaceId::from_uuid))
    }

    async fn space_of_attachment(
        &mut self,
        attachment_id: AttachmentId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        let space_id =
            sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM attachments WHERE id=$1")
                .bind(attachment_id.into_uuid())
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        Ok(space_id.map(SpaceId::from_uuid))
    }

    async fn insert_pairing(
        &mut self,
        pairing_id: Uuid,
        pairing: &Pairing,
        code_hash: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO computer_pairings\
             (id,code_hash,token_hash,hostname,os,daemon_version,status,expires_at,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)",
        )
        .bind(pairing_id)
        .bind(code_hash)
        .bind(&pairing.request.token_hash)
        .bind(&pairing.request.hostname)
        .bind(pairing.request.os.code())
        .bind(&pairing.request.daemon_version)
        .bind(pairing.status.code())
        .bind(pairing.expires_at)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn save_pairing(
        &mut self,
        pairing_id: Uuid,
        pairing: &Pairing,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "UPDATE computer_pairings SET status=$2,computer_id=$3,space_id=$4,\
             confirmed_at=CASE WHEN $2='confirmed' THEN $5 ELSE confirmed_at END WHERE id=$1",
        )
        .bind(pairing_id)
        .bind(pairing.status.code())
        .bind(pairing.computer_id.map(ComputerId::into_uuid))
        .bind(pairing.space_id.map(SpaceId::into_uuid))
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn pairing_by_code(
        &mut self,
        pairing_id: Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        let row = sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND code_hash=$2")
            .bind(pairing_id)
            .bind(code_hash)
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        row.map(|row| pairing_from_row(&row)).transpose()
    }

    async fn pairing_by_code_for_update(
        &mut self,
        pairing_id: Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        let row =
            sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND code_hash=$2 FOR UPDATE")
                .bind(pairing_id)
                .bind(code_hash)
                .fetch_optional(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
        row.map(|row| pairing_from_row(&row)).transpose()
    }

    async fn pairing_by_token(
        &mut self,
        pairing_id: Uuid,
        token_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        let row = sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND token_hash=$2")
            .bind(pairing_id)
            .bind(token_hash)
            .fetch_optional(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        row.map(|row| pairing_from_row(&row)).transpose()
    }

    async fn insert_computer(&mut self, record: &ComputerRecord) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO computers\
             (id,space_id,name,hostname,os,token_hash,connection_status,daemon_version,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,'offline',$7,$8)",
        )
        .bind(record.id.into_uuid())
        .bind(record.space_id.into_uuid())
        .bind(&record.name)
        .bind(&record.hostname)
        .bind(record.os.code())
        .bind(&record.token_hash)
        .bind(&record.daemon_version)
        .bind(record.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn paired_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<PairedComputer>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,name,hostname,os,connection_status,daemon_version,\
             last_seen_at,created_at,deleted_at FROM computers WHERE id=$1",
        )
        .bind(computer_id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        row.map(|row| paired_computer_from_row(&row)).transpose()
    }

    async fn space_computers(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<PairedComputer>, ApplicationError> {
        let rows = sqlx::query(
            "SELECT id,space_id,name,hostname,os,connection_status,daemon_version,\
             last_seen_at,created_at,deleted_at FROM computers WHERE space_id=$1 ORDER BY created_at",
        )
        .bind(space_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        rows.iter().map(paired_computer_from_row).collect()
    }

    async fn computer_for_token(
        &mut self,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<Option<bool>, ApplicationError> {
        let deleted = sqlx::query_scalar::<_, Option<OffsetDateTime>>(
            "SELECT deleted_at FROM computers WHERE id=$1 AND token_hash=$2",
        )
        .bind(computer_id.into_uuid())
        .bind(token_hash)
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(deleted.map(|deleted_at| deleted_at.is_some()))
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

    async fn attachment(
        &mut self,
        id: AttachmentId,
    ) -> Result<Option<Attachment>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,uploader_member_id,name,media_type,length,sha256,object_key,\
             status,created_at,ready_at FROM attachments WHERE id=$1 FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        row.map(|row| attachment_from_row(&row)).transpose()
    }

    async fn insert_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO attachments\
             (id,space_id,uploader_member_id,name,media_type,object_key,status,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
        )
        .bind(attachment.id.into_uuid())
        .bind(attachment.space_id.into_uuid())
        .bind(attachment.uploader_member_id.into_uuid())
        .bind(&attachment.name)
        .bind(&attachment.media_type)
        .bind(&attachment.object_key)
        .bind(attachment.status.code())
        .bind(attachment.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn save_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError> {
        let length = attachment
            .length
            .map(i64::try_from)
            .transpose()
            .map_err(|_| ApplicationError::PayloadTooLarge)?;
        sqlx::query(
            "UPDATE attachments SET name=$2,media_type=$3,length=$4,sha256=$5,status=$6,\
             ready_at=$7 WHERE id=$1",
        )
        .bind(attachment.id.into_uuid())
        .bind(&attachment.name)
        .bind(&attachment.media_type)
        .bind(length)
        .bind(attachment.sha256.map(|digest| digest.to_vec()))
        .bind(attachment.status.code())
        .bind(attachment.ready_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn attachment_is_visible(
        &mut self,
        id: AttachmentId,
        viewer: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM message_attachments links \
             JOIN messages ON messages.id=links.message_id \
             JOIN channel_members members ON members.channel_id=messages.channel_id \
             WHERE links.attachment_id=$1 AND members.member_id=$2)",
        )
        .bind(id.into_uuid())
        .bind(viewer.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    async fn record_attachment_write(
        &mut self,
        space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        attachment_id: AttachmentId,
        event_kind: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let attachment_uuid = attachment_id.into_uuid();
        sqlx::query(
            "INSERT INTO idempotency_records\
             (actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) \
             VALUES($1,$2,$3,'ok',$4,$5,$6)",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .bind(attachment_uuid)
        .bind(Sha256::digest(attachment_uuid.as_bytes()).as_slice())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO audit_events\
             (id,space_id,actor_member_id,action,subject_type,subject_id,created_at) \
             VALUES($1,$2,$3,$4,'attachment',$5,$6)",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(actor.into_uuid())
        .bind(action)
        .bind(attachment_uuid)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) \
             VALUES($1,$2,$3,$4,$5)",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(event_kind)
        .bind(json!({"attachment_id": attachment_uuid}))
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

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
                    action_channel_id, action_agent_member_id, created_at, edited_at, deleted_at \
             FROM messages WHERE thread_id = $1 AND placement = 'root' FOR UPDATE",
        )
        .bind(thread_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        message_from_row(&row)
    }

    async fn message(&mut self, id: MessageId) -> Result<Message, ApplicationError> {
        let row = sqlx::query("SELECT * FROM messages WHERE id=$1 FOR UPDATE")
            .bind(id.into_uuid())
            .fetch_one(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        message_from_row(&row)
    }

    async fn channel(&mut self, id: ChannelId) -> Result<Channel, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,kind,slug,topic,created_at FROM channels WHERE id=$1 FOR UPDATE",
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
            created_at: row.get("created_at"),
        })
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
            "SELECT id, space_id, connection_status, token_hash, deleted_at FROM computers WHERE id = $1 FOR UPDATE",
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
            } else if row.get::<&str, _>("connection_status") == "online" {
                ComputerLifecycle::Online
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
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<TaskId>, ApplicationError> {
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(format!("{}:{action}:{}", actor, key))
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        let value = sqlx::query_scalar::<_, Uuid>(
            "SELECT resource_id FROM idempotency_records \
             WHERE actor_member_id = $1 AND action = $2 AND idempotency_key = $3",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(value.map(TaskId::from_uuid))
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

    async fn can_manage_permissions(
        &mut self,
        actor: MemberId,
        target: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(\
               SELECT 1 FROM members actor JOIN members target ON target.space_id=actor.space_id \
               WHERE actor.id=$1 AND target.id=$2 AND actor.kind='human' \
                 AND actor.access_level IN ('owner','admin') AND actor.retired_at IS NULL \
                 AND target.retired_at IS NULL\
             )",
        )
        .bind(actor.into_uuid())
        .bind(target.into_uuid())
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

    async fn member_access_level(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<AccessLevel, ApplicationError> {
        let value = sqlx::query_scalar::<_, String>(
            "SELECT access_level FROM members WHERE id=$1 AND space_id=$2 AND retired_at IS NULL",
        )
        .bind(member_id.into_uuid())
        .bind(space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        access_level_from_str(&value)
    }

    async fn computer_accepts_agent(
        &mut self,
        computer_id: ComputerId,
        space_id: SpaceId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM computers WHERE id=$1 AND space_id=$2 \
             AND deleted_at IS NULL AND connection_status='online')",
        )
        .bind(computer_id.into_uuid())
        .bind(space_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    async fn thread_message_sequence(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<u64, ApplicationError> {
        let sequence = sqlx::query_scalar::<_, i64>(
            "SELECT channels.next_seq-1 FROM threads \
             JOIN channels ON channels.id=threads.channel_id \
             WHERE threads.id=$1 FOR UPDATE OF channels",
        )
        .bind(thread_id.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        u64::try_from(sequence).map_err(|_| ApplicationError::Internal)
    }

    async fn publish_message(
        &mut self,
        draft: MessageDraft,
    ) -> Result<PublishedMessage, ApplicationError> {
        sqlx::query("SET CONSTRAINTS ALL DEFERRED")
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
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
             content_kind,reply_to_message_id,author_member_id,body_markdown,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,'text',$7,$8,$9,$10)",
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
        .bind(draft.now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        if draft.thread_id.is_none() {
            sqlx::query(
                "INSERT INTO threads(id,space_id,channel_id,root_message_id,created_at) \
                 VALUES($1,$2,$3,$1,$4)",
            )
            .bind(thread_id.into_uuid())
            .bind(space_id)
            .bind(draft.channel_id.into_uuid())
            .bind(draft.now)
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
        let recipients = sqlx::query_scalar::<_, Uuid>(
            "SELECT members.id FROM channel_members JOIN members \
             ON members.id=channel_members.member_id WHERE channel_members.channel_id=$1 \
             AND members.kind='agent' AND members.id<>$2",
        )
        .bind(draft.channel_id.into_uuid())
        .bind(draft.author_member_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        let mentioned = draft.mentions.into_iter().collect::<BTreeSet<_>>();
        let mut hard_item_ids = Vec::new();
        for recipient in recipients {
            let agent_id = MemberId::from_uuid(recipient);
            let (kind, strength) = if task_id.is_some() {
                ("task_activity", "hard")
            } else if channel_kind == "direct" {
                ("direct", "hard")
            } else if mentioned.contains(&agent_id) {
                ("mention", "hard")
            } else if reply_author == Some(recipient) {
                ("reply", "hard")
            } else {
                ("channel_activity", "ambient")
            };
            let item_id = InboxItemId::from_uuid(Uuid::now_v7());
            sqlx::query(
                "INSERT INTO inbox_items(id,space_id,agent_id,message_id,thread_id,task_id,kind,\
                 strength,status,available_at,created_at) \
                 VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9,$9)",
            )
            .bind(item_id.into_uuid())
            .bind(space_id)
            .bind(recipient)
            .bind(draft.message_id.into_uuid())
            .bind(thread_id.into_uuid())
            .bind(task_id)
            .bind(kind)
            .bind(strength)
            .bind(draft.now)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
            if strength == "hard" {
                hard_item_ids.push(item_id);
            }
        }
        Ok(PublishedMessage {
            message_id: draft.message_id,
            hard_item_ids,
        })
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

    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError> {
        let changed = sqlx::query(
            "UPDATE inbox_items SET task_id=$2,status=$3,available_at=$4,lease_run_id=$5, \
             lease_expires_at=$6,retry_count=$7,handled_at=$8, \
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

    async fn save_message(&mut self, message: Message) -> Result<(), ApplicationError> {
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

    async fn grant_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
        granted_by: MemberId,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        sqlx::query(
            "INSERT INTO member_permissions(member_id,space_id,action_code,granted_by_member_id,created_at) \
             SELECT target.id,target.space_id,$2,$3,$4 FROM members target WHERE target.id=$1 \
             ON CONFLICT(member_id,action_code) DO NOTHING",
        )
        .bind(target.into_uuid())
        .bind(action.code())
        .bind(granted_by.into_uuid())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    async fn revoke_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
    ) -> Result<(), ApplicationError> {
        sqlx::query("DELETE FROM member_permissions WHERE member_id=$1 AND action_code=$2")
            .bind(target.into_uuid())
            .bind(action.code())
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
             VALUES ($1,$2,'agent',$3,$4,$5,$6)",
        )
        .bind(member.id.into_uuid())
        .bind(member.space_id.into_uuid())
        .bind(&member.display_name)
        .bind(&member.handle)
        .bind(access_level_str(member.access_level))
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
            sqlx::query("UPDATE members SET retired_at=$2 WHERE id=$1")
                .bind(agent.member_id.into_uuid())
                .bind(agent.retired_at)
                .execute(&mut *self.connection)
                .await
                .map_err(map_sqlx)?;
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
        action: &str,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError> {
        let hash = Sha256::digest(task_id.into_uuid().as_bytes());
        let response_code = if action == "task.create" {
            "created"
        } else {
            "ok"
        };
        sqlx::query(
            "INSERT INTO idempotency_records (actor_member_id,action,idempotency_key,response_code, \
             resource_id,result_hash,created_at) VALUES ($1,$2,$3,$4,$5,$6,now())",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .bind(response_code)
        .bind(task_id.into_uuid())
        .bind(hash.as_slice())
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
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

    async fn record_task_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        task_id: TaskId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let space_id = self.space_for_task(task_id).await?;
        sqlx::query(
            "INSERT INTO audit_events (id,space_id,actor_member_id,action,subject_type,subject_id,metadata_json,created_at) \
             VALUES ($1,$2,$3,$4,'task',$5,'{}',$6)",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(actor.into_uuid())
        .bind(action)
        .bind(task_id.into_uuid())
        .bind(now)
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

    async fn attention_notice(
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
        edited_at: row.get("edited_at"),
        deleted_at: row.get("deleted_at"),
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
        error_code: row
            .get::<Option<String>, _>("error_code")
            .map(|value| run_error_code_from_str(&value))
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
text_enum!(run_error_code_str, run_error_code_from_str, RunErrorCode, {
    RunErrorCode::InvalidCommand => "invalid_command", RunErrorCode::AgentUnavailable => "agent_unavailable", RunErrorCode::ProcessLost => "process_lost", RunErrorCode::SessionLost => "session_lost", RunErrorCode::SandboxUnavailable => "sandbox_unavailable", RunErrorCode::DriverUnavailable => "driver_unavailable", RunErrorCode::Internal => "internal"
});
text_enum!(disposition_str, disposition_from_str, InboxItemDisposition, {
    InboxItemDisposition::Handled => "handled", InboxItemDisposition::Deferred => "deferred", InboxItemDisposition::Released => "released"
});
text_enum!(inbox_status_str, inbox_status_from_str, InboxItemStatus, {
    InboxItemStatus::Pending => "pending", InboxItemStatus::Leased => "leased", InboxItemStatus::Deferred => "deferred", InboxItemStatus::Handled => "handled", InboxItemStatus::Dead => "dead"
});
fn inbox_kind_from_str(value: &str) -> Result<InboxItemKind, ApplicationError> {
    match value {
        "direct" => Ok(InboxItemKind::Direct),
        "mention" => Ok(InboxItemKind::Mention),
        "reply" => Ok(InboxItemKind::Reply),
        "task_activity" => Ok(InboxItemKind::TaskActivity),
        "thread_activity" => Ok(InboxItemKind::ThreadActivity),
        "channel_activity" => Ok(InboxItemKind::ChannelActivity),
        "system" => Ok(InboxItemKind::System),
        _ => Err(ApplicationError::Internal),
    }
}
fn strength_from_str(value: &str) -> Result<AttentionStrength, ApplicationError> {
    match value {
        "hard" => Ok(AttentionStrength::Hard),
        "ambient" => Ok(AttentionStrength::Ambient),
        _ => Err(ApplicationError::Internal),
    }
}
text_enum!(placement_str, placement_from_str, MessagePlacement, {
    MessagePlacement::Root => "root", MessagePlacement::Reply => "reply"
});
fn channel_kind_str(value: ChannelKind) -> &'static str {
    match value {
        ChannelKind::Public => "public",
        ChannelKind::Private => "private",
        ChannelKind::Direct => "direct",
    }
}

fn channel_kind_from_str(value: &str) -> Result<ChannelKind, ApplicationError> {
    match value {
        "public" => Ok(ChannelKind::Public),
        "private" => Ok(ChannelKind::Private),
        "direct" => Ok(ChannelKind::Direct),
        _ => Err(ApplicationError::Internal),
    }
}
text_enum!(agent_lifecycle_str, agent_lifecycle_from_str, AgentLifecycle, {
    AgentLifecycle::Provisioning => "provisioning", AgentLifecycle::Active => "active", AgentLifecycle::Suspended => "suspended", AgentLifecycle::Retired => "retired", AgentLifecycle::Error => "error"
});
text_enum!(driver_kind_str, driver_kind_from_str, DriverKind, {
    DriverKind::Codex => "codex", DriverKind::Builtin => "builtin"
});

fn permission_str(value: PermissionAction) -> &'static str {
    value.code()
}

fn access_level_str(value: AccessLevel) -> &'static str {
    match value {
        AccessLevel::Owner => "owner",
        AccessLevel::Admin => "admin",
        AccessLevel::Member => "member",
    }
}

fn attachment_from_row(row: &sqlx::postgres::PgRow) -> Result<Attachment, ApplicationError> {
    let length = row
        .get::<Option<i64>, _>("length")
        .map(u64::try_from)
        .transpose()
        .map_err(|_| ApplicationError::Internal)?;
    let sha256 = row
        .get::<Option<Vec<u8>>, _>("sha256")
        .map(<[u8; 32]>::try_from)
        .transpose()
        .map_err(|_| ApplicationError::Internal)?;
    Ok(Attachment {
        id: AttachmentId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        uploader_member_id: MemberId::from_uuid(row.get("uploader_member_id")),
        name: row.get("name"),
        media_type: row.get("media_type"),
        object_key: row.get("object_key"),
        status: AttachmentStatus::parse(row.get("status"))?,
        length,
        sha256,
        created_at: row.get("created_at"),
        ready_at: row.get("ready_at"),
    })
}

fn paired_computer_from_row(
    row: &sqlx::postgres::PgRow,
) -> Result<PairedComputer, ApplicationError> {
    Ok(PairedComputer {
        id: ComputerId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        name: row.get("name"),
        hostname: row.get("hostname"),
        os: ComputerOs::parse(row.get("os"))?,
        daemon_version: row.get("daemon_version"),
        connected: row.get::<String, _>("connection_status") == "online",
        deleted: row.get::<Option<OffsetDateTime>, _>("deleted_at").is_some(),
        last_seen_at: row.get("last_seen_at"),
        created_at: row.get("created_at"),
    })
}

fn pairing_from_row(row: &sqlx::postgres::PgRow) -> Result<Pairing, ApplicationError> {
    Ok(Pairing {
        request: PairingRequest {
            token_hash: row.get("token_hash"),
            hostname: row.get("hostname"),
            os: ComputerOs::parse(row.get("os"))?,
            daemon_version: row.get("daemon_version"),
        },
        status: PairingStatus::parse(row.get("status"))?,
        expires_at: row.get("expires_at"),
        computer_id: row
            .get::<Option<Uuid>, _>("computer_id")
            .map(ComputerId::from_uuid),
        space_id: row
            .get::<Option<Uuid>, _>("space_id")
            .map(SpaceId::from_uuid),
    })
}

fn access_level_from_str(value: &str) -> Result<AccessLevel, ApplicationError> {
    match value {
        "owner" => Ok(AccessLevel::Owner),
        "admin" => Ok(AccessLevel::Admin),
        "member" => Ok(AccessLevel::Member),
        _ => Err(ApplicationError::Internal),
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
    use crate::server::application::execution::{ClaimRun, ClaimRunInput};
    use crate::server::application::ports::RawFencingToken;
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
                idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
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

    #[tokio::test]
    async fn claim_run_inserts_the_run_before_leasing_its_inbox_item() {
        let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
            .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
        let database_name = format!("sumi_claim_run_{}", Uuid::now_v7().simple());
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
        let space_id = Uuid::now_v7();
        let owner_id = Uuid::now_v7();
        let agent_id = Uuid::now_v7();
        let computer_id = Uuid::now_v7();
        let channel_id = Uuid::now_v7();
        let message_id = Uuid::now_v7();
        let item_id = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,owner_member_id,created_at) VALUES ('{space_id}','claim-run','Claim Run','{owner_id}',now());
             INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{owner_id}','{space_id}','human','Owner','owner','owner',now());
             INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{agent_id}','{space_id}','agent','Agent','agent','member',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space_id}','Computer','localhost','linux','claim-run-hash','online',1,now());
             INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent_id}','{space_id}','{computer_id}','Reply',1,'active','codex',now());
             INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel_id}','{space_id}','public','general',2,now());
             INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel_id}','{space_id}','{owner_id}',now(),0),('{channel_id}','{space_id}','{agent_id}',now(),0);
             INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{message_id}','{space_id}','{channel_id}','{message_id}',1,'root','text','{owner_id}','mention',now());
             INSERT INTO threads(id,space_id,channel_id,root_message_id,created_at) VALUES ('{message_id}','{space_id}','{channel_id}','{message_id}',now());
             INSERT INTO inbox_items(id,space_id,agent_id,message_id,thread_id,kind,strength,status,available_at,last_error_code,created_at) VALUES ('{item_id}','{space_id}','{agent_id}','{message_id}','{message_id}','mention','hard','pending',now(),'run_claim_unavailable',now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

        let run_id = RunId::from_uuid(Uuid::now_v7());
        ClaimRun::execute(
            &mut adapter,
            ClaimRunInput {
                run_id,
                agent_id: MemberId::from_uuid(agent_id),
                computer_id: ComputerId::from_uuid(computer_id),
                task_id: None,
                focus_thread_id: ThreadId::from_uuid(message_id),
                item_ids: vec![InboxItemId::from_uuid(item_id)],
                fencing_token: RawFencingToken::new("claim-run-token".to_owned()),
                lease_expires_at: OffsetDateTime::now_utc() + time::Duration::minutes(2),
            },
        )
        .await
        .unwrap();

        let facts: (String, String, Option<String>, i64, i64, i64) = sqlx::query_as(
            "SELECT r.status,i.status,i.last_error_code, \
             (SELECT count(*) FROM run_items WHERE run_id=r.id), \
             (SELECT count(*) FROM computer_commands WHERE kind='run.start'), \
             (SELECT count(*) FROM outbox_events WHERE kind='message.updated') \
             FROM agent_runs r JOIN inbox_items i ON i.lease_run_id=r.id WHERE r.id=$1",
        )
        .bind(run_id.into_uuid())
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(facts, ("queued".into(), "leased".into(), None, 1, 1, 1));

        pool.close().await;
        sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
            .execute(&mut admin)
            .await
            .unwrap();
    }
}
