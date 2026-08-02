use std::{str::FromStr, sync::Arc};

use axum::http::{HeaderMap, HeaderValue};
use object_store::local::LocalFileSystem;
use sqlx::{Connection, PgConnection, postgres::PgConnectOptions};
use tempfile::TempDir;
use url::Url;

use super::*;
use crate::ids::{AgentId, ChannelId, EventId, IdempotencyKey, SpaceId};
use crate::protocol::computer::{
    FencingToken, ItemDisposition, ItemOutcome, RunResult, RunTerminalStatus,
};

struct CapabilityFixture {
    state: RuntimeState,
    admin: PgConnection,
    database_name: String,
    _objects: TempDir,
    headers: HeaderMap,
    computer_id: Uuid,
    owner_id: Uuid,
    channel_id: Uuid,
    context: capability::RunContext,
    handled_item_id: InboxItemId,
    deferred_item_id: InboxItemId,
}

impl CapabilityFixture {
    async fn create() -> Self {
        let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
            .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
        let database_name = format!("sumi_capability_{}", Uuid::now_v7().simple());
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
        let storage = PostgresAdapter::new(pool.clone());
        storage.initialize_schema().await.unwrap();

        let space_id = Uuid::now_v7();
        let owner_id = Uuid::now_v7();
        let agent_id = Uuid::now_v7();
        let computer_id = Uuid::now_v7();
        let channel_id = Uuid::now_v7();
        let focus_id = Uuid::now_v7();
        let run_id = Uuid::now_v7();
        let handled_item_id = Uuid::now_v7();
        let deferred_item_id = Uuid::now_v7();
        let computer_token = "capability-computer-token";
        let fencing_token = "capability-fencing-token";
        sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at) VALUES ('{space_id}','capability','Capability','#FE7DA8','{owner_id}',now());
                 INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{owner_id}','{space_id}','human','Owner','owner','owner',now());
                 INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{agent_id}','{space_id}','agent','Agent','agent','member',now());
                 INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space_id}','Computer','localhost','linux','{}','online',1,now());
                 INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent_id}','{space_id}','{computer_id}','Test',1,'active','codex',now());
                 INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel_id}','{space_id}','private','general',2,now());
                 INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel_id}','{space_id}','{owner_id}',now(),0);
                 INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel_id}','{space_id}','{agent_id}',now(),0);
                 INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{focus_id}','{space_id}','{channel_id}','{focus_id}',1,'root','text','{owner_id}','source',now());
                 INSERT INTO threads(id,space_id,channel_id,root_message_id,created_at) VALUES ('{focus_id}','{space_id}','{channel_id}','{focus_id}',now());
                 INSERT INTO agent_runs(id,space_id,agent_id,focus_thread_id,status,fencing_token_hash,lease_expires_at,created_at,started_at) VALUES ('{run_id}','{space_id}','{agent_id}','{focus_id}','running','{}',now()+interval '1 hour',now(),now());
                 INSERT INTO inbox_items(id,space_id,member_id,thread_id,kind,strength,status,available_at,lease_run_id,lease_expires_at,created_at) VALUES
                   ('{handled_item_id}','{space_id}','{agent_id}','{focus_id}','mention','hard','leased',now(),'{run_id}',now()+interval '1 hour',now()),
                   ('{deferred_item_id}','{space_id}','{agent_id}','{focus_id}','mention','hard','leased',now(),'{run_id}',now()+interval '1 hour',now());
                 INSERT INTO run_items(run_id,inbox_item_id,delivery_seq,attached_at) VALUES
                   ('{run_id}','{handled_item_id}',1,now()),
                   ('{run_id}','{deferred_item_id}',2,now());
                 COMMIT;",
                token_hash(computer_token),
                token_hash(fencing_token),
            ))
            .execute(&pool)
            .await
            .unwrap();

        let objects = tempfile::tempdir().unwrap();
        let object_store = LocalFileSystem::new_with_prefix(objects.path()).unwrap();
        let state = RuntimeState {
            pool,
            storage,
            objects: Arc::new(AttachmentObjectStore::new(Arc::new(object_store))),
            session_lifetime: SessionLifetime::from_hours(1).unwrap(),
            attachment_max_bytes: 100 * 1024 * 1024,
            queries: QueryRegistry::default(),
        };
        let mut headers = HeaderMap::new();
        headers.insert(
            "Authorization",
            HeaderValue::from_str(&format!("Bearer {computer_token}")).unwrap(),
        );
        Self {
            state,
            admin,
            database_name,
            _objects: objects,
            headers,
            computer_id,
            owner_id,
            channel_id,
            context: capability::RunContext {
                agent_id: AgentId::from_uuid(agent_id),
                space_id: SpaceId::from_uuid(space_id),
                task_id: None,
                focus_thread_id: ThreadId::from_uuid(focus_id),
                run_id: RunId::from_uuid(run_id),
                fencing_token: fencing_token.to_owned(),
                message_snapshot_sequence: 1,
            },
            handled_item_id: InboxItemId::from_uuid(handled_item_id),
            deferred_item_id: InboxItemId::from_uuid(deferred_item_id),
        }
    }

    async fn execute(&self, action: capability::Action) -> Result<Value, capability::Error> {
        self.execute_with_key(action, IdempotencyKey::from_uuid(Uuid::now_v7()))
            .await
    }

    async fn execute_with_key(
        &self,
        action: capability::Action,
        idempotency_key: IdempotencyKey,
    ) -> Result<Value, capability::Error> {
        execute_agent_action(
            &self.state,
            &self.headers,
            self.computer_id,
            AgentActionRequest {
                context: self.context.clone(),
                action,
                idempotency_key: Some(idempotency_key),
            },
        )
        .await
    }

    async fn bind_task(&mut self) -> TaskId {
        let created = self
            .execute(capability::Action::TaskCreate {
                title: Some("Capability Task".into()),
                assignee: None,
            })
            .await
            .unwrap();
        let task_id = TaskId::from_uuid(Uuid::parse_str(created["id"].as_str().unwrap()).unwrap());
        self.context.task_id = Some(task_id);
        task_id
    }

    async fn destroy(mut self) {
        self.state.pool.close().await;
        sqlx::query(&format!(
            "DROP DATABASE \"{}\" WITH (FORCE)",
            self.database_name
        ))
        .execute(&mut self.admin)
        .await
        .unwrap();
    }
}

#[tokio::test]
async fn agent_activity_and_last_error_code_come_from_run_and_inbox_facts() {
    let fixture = CapabilityFixture::create().await;
    let agent_id = fixture.context.agent_id.into_uuid();
    let focus_id = fixture.context.focus_thread_id.into_uuid();

    async fn read_agent(fixture: &CapabilityFixture, agent_id: Uuid) -> AgentResponse {
        let row = sqlx::query(&format!(
            "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,\
                 c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
                 FROM agents a JOIN members m ON m.id=a.member_id \
                 LEFT JOIN computers c ON c.id=a.computer_id {ACTIVITY_JOINS} \
                 WHERE a.member_id=$1"
        ))
        .bind(agent_id)
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        agent_row(&row).unwrap()
    }

    let initial = read_agent(&fixture, agent_id).await;
    assert_eq!(initial.last_error_code, None);

    let run_id = fixture.context.run_id.into_uuid();
    let running = read_agent(&fixture, agent_id).await;
    let activity = running.activity.as_ref().unwrap();
    assert_eq!(activity.kind, "running");
    assert!(activity.label.contains("#general:1"), "{}", activity.label);
    assert!(matches!(
        running.activity_status,
        AgentActivityStatus::Running
    ));

    let item_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,kind,strength,\
             status,available_at,last_error_code,created_at) \
             VALUES($1,$2,$3,$4,$4,'mention','hard','pending',now(),'run_claim_unavailable',now())",
    )
    .bind(item_id)
    .bind(fixture.context.space_id.into_uuid())
    .bind(agent_id)
    .bind(focus_id)
    .execute(&fixture.state.pool)
    .await
    .unwrap();
    let with_item_error = read_agent(&fixture, agent_id).await;
    assert_eq!(
        with_item_error.last_error_code.as_deref(),
        Some("run_claim_unavailable")
    );

    sqlx::query(
        "UPDATE agent_runs SET status='failed',outcome_code='failed',\
             error_code='session_lost',finished_at=now() WHERE id=$1",
    )
    .bind(run_id)
    .execute(&fixture.state.pool)
    .await
    .unwrap();
    let failed = read_agent(&fixture, agent_id).await;
    assert_eq!(failed.last_error_code.as_deref(), Some("session_lost"));
    assert!(failed.activity.is_none());

    fixture.destroy().await;
}

#[tokio::test]
async fn message_projection_returns_persisted_mentions_and_mention_all() {
    let fixture = CapabilityFixture::create().await;
    let message_id = fixture.context.focus_thread_id.into_uuid();
    sqlx::query("UPDATE messages SET mention_all=true WHERE id=$1")
        .bind(message_id)
        .execute(&fixture.state.pool)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO message_mentions(message_id,space_id,member_id,created_at) \
         VALUES($1,$2,$3,now())",
    )
    .bind(message_id)
    .bind(fixture.context.space_id.into_uuid())
    .bind(fixture.context.agent_id.into_uuid())
    .execute(&fixture.state.pool)
    .await
    .unwrap();
    let row = sqlx::query("SELECT * FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
    let projected = message_row(&fixture.state.pool, &row).await.unwrap();
    assert_eq!(
        projected.mentions,
        vec![fixture.context.agent_id.into_uuid()]
    );
    assert!(projected.mention_all);
    fixture.destroy().await;
}

#[tokio::test]
async fn run_claim_failure_is_projected_once_on_its_source_message() {
    let fixture = CapabilityFixture::create().await;
    let item_id = Uuid::now_v7();
    let message_id = fixture.context.focus_thread_id.into_uuid();
    sqlx::query(
            "INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,kind,strength, \
             status,available_at,created_at) VALUES($1,$2,$3,$4,$4,'mention','hard','pending',now(),now())",
        )
        .bind(item_id)
        .bind(fixture.context.space_id.into_uuid())
        .bind(fixture.context.agent_id.into_uuid())
        .bind(message_id)
        .execute(&fixture.state.pool)
        .await
        .unwrap();
    sqlx::query("UPDATE messages SET mention_all=true WHERE id=$1")
        .bind(message_id)
        .execute(&fixture.state.pool)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO message_mentions(message_id,space_id,member_id,created_at) \
         VALUES($1,$2,$3,now())",
    )
    .bind(message_id)
    .bind(fixture.context.space_id.into_uuid())
    .bind(fixture.context.agent_id.into_uuid())
    .execute(&fixture.state.pool)
    .await
    .unwrap();

    let mut storage = fixture.state.storage.clone();
    assert!(
        ClaimNextRun::record_failure(
            &mut storage,
            InboxItemId::from_uuid(item_id),
            Some(MessageId::from_uuid(message_id)),
            ChannelId::from_uuid(fixture.channel_id),
            "run_claim_unavailable",
        )
        .await
        .unwrap()
    );
    let mut storage = fixture.state.storage.clone();
    assert!(
        !ClaimNextRun::record_failure(
            &mut storage,
            InboxItemId::from_uuid(item_id),
            Some(MessageId::from_uuid(message_id)),
            ChannelId::from_uuid(fixture.channel_id),
            "run_claim_unavailable",
        )
        .await
        .unwrap()
    );

    let row = sqlx::query("SELECT * FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
    let projected = message_row(&fixture.state.pool, &row).await.unwrap();
    let failures = &projected.attention_failures;
    assert_eq!(failures.len(), 1);
    assert_eq!(
        failures[0].agent_member_id,
        fixture.context.agent_id.into_uuid()
    );
    assert_eq!(failures[0].agent_handle, "agent");
    assert_eq!(failures[0].error_code, "run_claim_unavailable");
    assert!(failures[0].retrying);
    assert_eq!(
        projected.mentions,
        vec![fixture.context.agent_id.into_uuid()]
    );
    assert!(projected.mention_all);
    let event_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM outbox_events WHERE kind='message.updated' \
             AND payload_json->>'resource_id'=$1",
    )
    .bind(message_id.to_string())
    .fetch_one(&fixture.state.pool)
    .await
    .unwrap();
    assert_eq!(event_count, 1);
    fixture.destroy().await;
}

#[tokio::test]
async fn message_hard_items_attach_same_focus_and_notice_different_focus() {
    let fixture = CapabilityFixture::create().await;
    let focus_id = fixture.context.focus_thread_id.into_uuid();
    insert_message(
        &fixture.state,
        fixture.channel_id,
        fixture.owner_id,
        MessageWriteContext {
            idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
            thread_id: Some(focus_id),
            handled_item: None,
            expected_snapshot: None,
        },
        CreateMessageBody {
            body_markdown: "same Focus".into(),
            mentions: vec![fixture.context.agent_id.into_uuid()],
            mention_all: false,
            attachment_ids: Vec::new(),
            reply_to_message_id: None,
        },
    )
    .await
    .unwrap();
    let same_focus: (Uuid, String, Option<Uuid>, i64, i64) = sqlx::query_as(
        "SELECT i.id,i.status,i.lease_run_id,ri.delivery_seq, \
             (SELECT count(*) FROM computer_commands WHERE kind='run.attach_item') \
             FROM inbox_items i JOIN run_items ri ON ri.inbox_item_id=i.id \
             WHERE i.message_id=(SELECT id FROM messages WHERE body_markdown='same Focus')",
    )
    .fetch_one(&fixture.state.pool)
    .await
    .unwrap();
    assert_eq!(same_focus.1, "leased");
    assert_eq!(same_focus.2, Some(fixture.context.run_id.into_uuid()));
    assert_eq!(same_focus.3, 3);
    assert_eq!(same_focus.4, 1);
    let mut storage = fixture.state.storage.clone();
    RouteHardItem::execute(
        &mut storage,
        RouteHardItemInput {
            item_id: InboxItemId::from_uuid(same_focus.0),
        },
    )
    .await
    .unwrap();
    let attach_commands: i64 =
        sqlx::query_scalar("SELECT count(*) FROM computer_commands WHERE kind='run.attach_item'")
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap();
    assert_eq!(attach_commands, 1);

    insert_message(
        &fixture.state,
        fixture.channel_id,
        fixture.owner_id,
        MessageWriteContext {
            idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
            thread_id: None,
            handled_item: None,
            expected_snapshot: None,
        },
        CreateMessageBody {
            body_markdown: "different Focus".into(),
            mentions: vec![fixture.context.agent_id.into_uuid()],
            mention_all: false,
            attachment_ids: Vec::new(),
            reply_to_message_id: None,
        },
    )
    .await
    .unwrap();
    let different_focus: (String, i64, Value) = sqlx::query_as(
            "SELECT i.status, \
             (SELECT count(*) FROM computer_commands WHERE kind='run.notice'), \
             (SELECT payload_json FROM computer_commands WHERE kind='run.notice' ORDER BY computer_seq DESC LIMIT 1) \
             FROM inbox_items i WHERE i.message_id=(SELECT id FROM messages WHERE body_markdown='different Focus')",
        )
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
    assert_eq!(different_focus.0, "pending");
    assert_eq!(different_focus.1, 1);
    assert!(!different_focus.2.to_string().contains("different Focus"));
    assert!(!different_focus.2.to_string().contains("body_markdown"));
    sqlx::query("UPDATE agent_runs SET status='finalizing' WHERE id=$1")
        .bind(fixture.context.run_id.into_uuid())
        .execute(&fixture.state.pool)
        .await
        .unwrap();
    insert_message(
        &fixture.state,
        fixture.channel_id,
        fixture.owner_id,
        MessageWriteContext {
            idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
            thread_id: Some(focus_id),
            handled_item: None,
            expected_snapshot: None,
        },
        CreateMessageBody {
            body_markdown: "finalizing race".into(),
            mentions: vec![fixture.context.agent_id.into_uuid()],
            mention_all: false,
            attachment_ids: Vec::new(),
            reply_to_message_id: None,
        },
    )
    .await
    .unwrap();
    let finalizing: (String, i64, i64) = sqlx::query_as(
            "SELECT i.status, \
             (SELECT count(*) FROM computer_commands WHERE kind='run.attach_item'), \
             (SELECT count(*) FROM computer_commands WHERE kind='run.notice') \
             FROM inbox_items i WHERE i.message_id=(SELECT id FROM messages WHERE body_markdown='finalizing race')",
        )
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
    assert_eq!(finalizing, ("pending".into(), 1, 1));
    let events = fixture
        .state
        .storage
        .browser_events(
            fixture.context.space_id,
            MemberId::from_uuid(fixture.owner_id),
            None,
        )
        .await
        .unwrap()
        .unwrap();
    assert_eq!(
        events
            .iter()
            .filter(|event| event.event_type == "message.created")
            .count(),
        3
    );
    let last_event_id = events.last().unwrap().event_id;
    assert!(
        fixture
            .state
            .storage
            .browser_events(
                fixture.context.space_id,
                MemberId::from_uuid(fixture.owner_id),
                Some(last_event_id),
            )
            .await
            .unwrap()
            .unwrap()
            .is_empty()
    );
    fixture.destroy().await;
}

#[tokio::test]
async fn agent_retirement_cancels_run_before_computer_deletion_revokes_token() {
    let fixture = CapabilityFixture::create().await;
    let mut storage = fixture.state.storage.clone();
    RetireAgent::execute(
        &mut storage,
        MemberId::from_uuid(fixture.owner_id),
        MemberId::from_uuid(fixture.context.agent_id.into_uuid()),
        IdempotencyKey::from_uuid(Uuid::now_v7()),
        OffsetDateTime::now_utc(),
    )
    .await
    .unwrap();
    let retired: (String, Option<Uuid>, String, i64, i64) = sqlx::query_as(
        "SELECT a.lifecycle,a.computer_id,r.status, \
             (SELECT count(*) FROM members WHERE id=a.member_id AND retired_at IS NOT NULL), \
             (SELECT count(*) FROM computer_commands WHERE kind='agent.retire') \
             FROM agents a JOIN agent_runs r ON r.agent_id=a.member_id WHERE a.member_id=$1",
    )
    .bind(fixture.context.agent_id.into_uuid())
    .fetch_one(&fixture.state.pool)
    .await
    .unwrap();
    assert_eq!(retired, ("retired".into(), None, "canceled".into(), 1, 1));

    DeleteComputer::execute(
        &mut storage,
        MemberId::from_uuid(fixture.owner_id),
        ComputerId::from_uuid(fixture.computer_id),
        IdempotencyKey::from_uuid(Uuid::now_v7()),
        OffsetDateTime::now_utc(),
    )
    .await
    .unwrap();
    let deleted: (Option<String>, Option<OffsetDateTime>) =
        sqlx::query_as("SELECT token_hash,deleted_at FROM computers WHERE id=$1")
            .bind(fixture.computer_id)
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap();
    assert_eq!(deleted.0, None);
    assert!(deleted.1.is_some());
    fixture.destroy().await;
}

#[tokio::test]
async fn capability_dispositions_are_atomic_idempotent_and_conflict_safe() {
    let fixture = CapabilityFixture::create().await;
    sqlx::raw_sql(
        "CREATE FUNCTION test_reject_run_item_disposition() RETURNS trigger LANGUAGE plpgsql AS $$
             BEGIN RAISE EXCEPTION 'forced capability rollback'; END $$;
             CREATE CONSTRAINT TRIGGER test_reject_run_item_disposition
             AFTER UPDATE OF disposition ON run_items DEFERRABLE INITIALLY DEFERRED
             FOR EACH ROW EXECUTE FUNCTION test_reject_run_item_disposition();",
    )
    .execute(&fixture.state.pool)
    .await
    .unwrap();
    let failed = fixture
        .execute(capability::Action::MessageSend(capability::MessageSend {
            target: capability::MessageTarget::Focus,
            body: "must roll back".to_owned(),
            handle_item_id: Some(fixture.handled_item_id),
            snapshot_sequence: None,
        }))
        .await
        .unwrap_err();
    assert_eq!(failed.code, capability::ErrorCode::Internal);
    let rolled_back: (i64, Option<String>) = (
        sqlx::query_scalar("SELECT count(*) FROM messages WHERE body_markdown='must roll back'")
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap(),
        sqlx::query_scalar(
            "SELECT disposition FROM run_items WHERE run_id=$1 AND inbox_item_id=$2",
        )
        .bind(fixture.context.run_id.into_uuid())
        .bind(fixture.handled_item_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap(),
    );
    assert_eq!(rolled_back, (0, None));
    sqlx::raw_sql(
        "DROP TRIGGER test_reject_run_item_disposition ON run_items;
             DROP FUNCTION test_reject_run_item_disposition();",
    )
    .execute(&fixture.state.pool)
    .await
    .unwrap();

    let ack = capability::Action::InboxAck {
        item_id: fixture.handled_item_id,
        reason: Some("handled".to_owned()),
    };
    fixture.execute(ack.clone()).await.unwrap();
    fixture.execute(ack).await.unwrap();
    let defer_until = OffsetDateTime::now_utc() + Duration::hours(2);
    let defer = capability::Action::InboxDefer {
        item_id: fixture.deferred_item_id,
        until: defer_until,
    };
    fixture.execute(defer.clone()).await.unwrap();
    fixture.execute(defer).await.unwrap();

    let facts: (String, String, String, String) = sqlx::query_as(
        "SELECT handled.disposition,deferred.disposition,handled_item.status,deferred_item.status
             FROM run_items handled
             JOIN run_items deferred ON deferred.run_id=handled.run_id
             JOIN inbox_items handled_item ON handled_item.id=handled.inbox_item_id
             JOIN inbox_items deferred_item ON deferred_item.id=deferred.inbox_item_id
             WHERE handled.inbox_item_id=$1 AND deferred.inbox_item_id=$2",
    )
    .bind(fixture.handled_item_id.into_uuid())
    .bind(fixture.deferred_item_id.into_uuid())
    .fetch_one(&fixture.state.pool)
    .await
    .unwrap();
    assert_eq!(
        facts,
        (
            "handled".into(),
            "deferred".into(),
            "leased".into(),
            "leased".into()
        )
    );

    let conflict = fixture
        .execute(capability::Action::InboxDefer {
            item_id: fixture.handled_item_id,
            until: defer_until,
        })
        .await
        .unwrap_err();
    assert_eq!(conflict.code, capability::ErrorCode::Conflict);

    let submitted = super::super::http::submit_run_result(
        &fixture.state.storage,
        super::super::http::ComputerPrincipal {
            computer_id: ComputerId::from_uuid(fixture.computer_id),
        },
        RunResult {
            event_id: EventId::from_uuid(Uuid::now_v7()),
            run_id: fixture.context.run_id,
            fencing_token: FencingToken::new(fixture.context.fencing_token.clone()),
            status: RunTerminalStatus::Yielded,
            item_outcomes: vec![
                ItemOutcome {
                    item_id: fixture.handled_item_id,
                    disposition: ItemDisposition::Handled,
                },
                ItemOutcome {
                    item_id: fixture.deferred_item_id,
                    disposition: ItemDisposition::Deferred,
                },
            ],
            continuation_note: Some("continue later".to_owned()),
            error_code: None,
        },
    )
    .await;
    assert!(submitted.is_ok());
    let yielded: (String, Option<String>, String, String) = sqlx::query_as(
        "SELECT runs.status,runs.continuation_note,handled.status,deferred.status
             FROM agent_runs runs
             JOIN inbox_items handled ON handled.lease_run_id IS NULL AND handled.id=$2
             JOIN inbox_items deferred ON deferred.lease_run_id IS NULL AND deferred.id=$3
             WHERE runs.id=$1",
    )
    .bind(fixture.context.run_id.into_uuid())
    .bind(fixture.handled_item_id.into_uuid())
    .bind(fixture.deferred_item_id.into_uuid())
    .fetch_one(&fixture.state.pool)
    .await
    .unwrap();
    assert_eq!(
        yielded,
        (
            "yielded".into(),
            Some("continue later".into()),
            "handled".into(),
            "deferred".into()
        )
    );
    fixture.destroy().await;
}

#[tokio::test]
async fn capability_task_done_commits_collaboration_facts_and_replays() {
    let mut fixture = CapabilityFixture::create().await;
    let task_id = fixture.bind_task().await;
    let key = IdempotencyKey::from_uuid(Uuid::now_v7());
    let action = capability::Action::TaskDone {
        result: "Task Result".into(),
        post_to: capability::PostTarget::Focus,
    };

    sqlx::raw_sql(
        "CREATE FUNCTION test_reject_task_audit() RETURNS trigger LANGUAGE plpgsql AS $$
             BEGIN RAISE EXCEPTION 'forced Task transaction rollback'; END $$;
             CREATE CONSTRAINT TRIGGER test_reject_task_audit
             AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED
             FOR EACH ROW EXECUTE FUNCTION test_reject_task_audit();",
    )
    .execute(&fixture.state.pool)
    .await
    .unwrap();
    let failed = fixture
        .execute_with_key(action.clone(), key)
        .await
        .unwrap_err();
    assert_eq!(failed.code, capability::ErrorCode::Internal);
    let rolled_back: (String, String, i64, i64) = sqlx::query_as(
        "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM messages WHERE body_markdown='Task Result'), \
             (SELECT count(*) FROM inbox_items WHERE status='leased' AND lease_run_id=$2) \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
    )
    .bind(task_id.into_uuid())
    .bind(fixture.context.run_id.into_uuid())
    .fetch_one(&fixture.state.pool)
    .await
    .unwrap();
    assert_eq!(rolled_back, ("in_progress".into(), "running".into(), 0, 2));
    sqlx::raw_sql(
        "DROP TRIGGER test_reject_task_audit ON audit_events;
             DROP FUNCTION test_reject_task_audit();",
    )
    .execute(&fixture.state.pool)
    .await
    .unwrap();

    fixture.execute_with_key(action.clone(), key).await.unwrap();
    fixture.execute_with_key(action, key).await.unwrap();

    let facts: (String, String, i64, i64, i64, i64, i64) = sqlx::query_as(
            "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM inbox_items WHERE lease_run_id IS NULL AND status='handled' AND id IN ($2,$3)), \
             (SELECT count(*) FROM messages WHERE body_markdown='Task Result'), \
             (SELECT count(*) FROM audit_events WHERE action='task.done' AND subject_id=$1), \
             (SELECT count(*) FROM idempotency_records WHERE action='task.done' AND resource_id=$1), \
             (SELECT count(*) FROM computer_commands WHERE kind='session.close') \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
        )
        .bind(task_id.into_uuid())
        .bind(fixture.handled_item_id.into_uuid())
        .bind(fixture.deferred_item_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
    assert_eq!(facts, ("done".into(), "completed".into(), 2, 1, 1, 1, 1));
    fixture.destroy().await;
}

#[tokio::test]
async fn capability_task_submit_review_and_close_use_terminal_transaction() {
    let mut review = CapabilityFixture::create().await;
    let review_task = review.bind_task().await;
    review
        .execute(capability::Action::TaskSubmitReview {
            body: "Ready for review".into(),
            post_to: capability::PostTarget::Source,
        })
        .await
        .unwrap();
    let review_facts: (String, String, i64) = sqlx::query_as(
        "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM messages WHERE body_markdown='Ready for review') \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
    )
    .bind(review_task.into_uuid())
    .fetch_one(&review.state.pool)
    .await
    .unwrap();
    assert_eq!(review_facts, ("in_review".into(), "completed".into(), 1));
    review.destroy().await;

    let mut close = CapabilityFixture::create().await;
    let close_task = close.bind_task().await;
    close
        .execute(capability::Action::TaskClose {
            reason: capability::CloseReason::Obsolete,
            note: Some("No longer needed".into()),
        })
        .await
        .unwrap();
    let close_facts: (String, String, String, Option<String>, i64) = sqlx::query_as(
        "SELECT tasks.status,runs.status,tasks.close_reason_code,tasks.close_reason_note, \
             (SELECT count(*) FROM audit_events WHERE action='task.close' AND subject_id=$1) \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
    )
    .bind(close_task.into_uuid())
    .fetch_one(&close.state.pool)
    .await
    .unwrap();
    assert_eq!(
        close_facts,
        (
            "closed".into(),
            "completed".into(),
            "obsolete".into(),
            Some("No longer needed".into()),
            1
        )
    );
    close.destroy().await;
}

#[tokio::test]
async fn agent_attachment_stream_uses_active_run_and_commits_metadata() {
    let fixture = CapabilityFixture::create().await;
    let mut headers = fixture.headers.clone();
    headers.insert(
        "x-sumi-fencing-token",
        HeaderValue::from_str(&fixture.context.fencing_token).unwrap(),
    );
    let key = Uuid::now_v7();
    headers.insert(
        "idempotency-key",
        HeaderValue::from_str(&key.to_string()).unwrap(),
    );
    let path = (
        fixture.computer_id,
        fixture.context.agent_id.into_uuid(),
        fixture.context.run_id.into_uuid(),
    );
    let created = agent_create_upload(
        State(fixture.state.clone()),
        headers.clone(),
        Path(path),
        Json(AgentCreateUploadBody {
            original_name: "result.txt".into(),
            media_type: "text/plain".into(),
        }),
    )
    .await
    .unwrap();
    let attachment_id = created.1.0.id;
    let replayed = agent_create_upload(
        State(fixture.state.clone()),
        headers.clone(),
        Path(path),
        Json(AgentCreateUploadBody {
            original_name: "result.txt".into(),
            media_type: "text/plain".into(),
        }),
    )
    .await
    .unwrap();
    assert_eq!(replayed.1.0.id, attachment_id);

    let content = Bytes::from_static(b"agent attachment payload");
    let attachment_path = (path.0, path.1, path.2, attachment_id);
    agent_upload_content(
        State(fixture.state.clone()),
        headers.clone(),
        Path(attachment_path),
        content.clone(),
    )
    .await
    .unwrap();
    let digest = hex::encode(Sha256::digest(&content));
    let _ = agent_complete_upload(
        State(fixture.state.clone()),
        headers.clone(),
        Path(attachment_path),
        Json(CompleteUploadBody {
            size: content.len() as u64,
            sha256: digest.clone(),
        }),
    )
    .await
    .unwrap();
    let completed_again = agent_complete_upload(
        State(fixture.state.clone()),
        headers.clone(),
        Path(attachment_path),
        Json(CompleteUploadBody {
            size: content.len() as u64,
            sha256: digest,
        }),
    )
    .await
    .unwrap();
    assert!(matches!(completed_again.0.status, AttachmentStatus::Ready));
    let downloaded =
        agent_download_attachment(State(fixture.state.clone()), headers, Path(attachment_path))
            .await
            .unwrap();
    assert_eq!(downloaded, content);

    let records: (i64, i64, i64) = sqlx::query_as(
        "SELECT \
             (SELECT count(*) FROM idempotency_records WHERE resource_id=$1), \
             (SELECT count(*) FROM audit_events WHERE subject_id=$1), \
             (SELECT count(*) FROM outbox_events WHERE payload_json->>'attachment_id'=$1::text)",
    )
    .bind(attachment_id)
    .fetch_one(&fixture.state.pool)
    .await
    .unwrap();
    assert_eq!(records, (2, 2, 2));
    fixture.destroy().await;
}

#[tokio::test]
async fn channel_read_is_authorized_and_stale_writes_are_rejected() {
    let mut fixture = CapabilityFixture::create().await;
    let channel_id: Uuid = sqlx::query_scalar("SELECT channel_id FROM threads WHERE id=$1")
        .bind(fixture.context.focus_thread_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
    let read = fixture
        .execute(capability::Action::ChannelRead {
            channel_id: ChannelId::from_uuid(channel_id),
            around_message_id: None,
            limit: 20,
        })
        .await
        .unwrap();
    assert_eq!(read["snapshot_channel_seq"], 1);
    assert_eq!(read["messages"].as_array().unwrap().len(), 1);

    let task_id = fixture.bind_task().await;
    let new_message_id = Uuid::now_v7();
    sqlx::query("UPDATE channels SET next_seq=next_seq+1 WHERE id=$1")
        .bind(channel_id)
        .execute(&fixture.state.pool)
        .await
        .unwrap();
    sqlx::query("INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES($1,$2,$3,$4,2,'reply','text',$5,'new context',now())")
            .bind(new_message_id)
            .bind(fixture.context.space_id.into_uuid())
            .bind(channel_id)
            .bind(fixture.context.focus_thread_id.into_uuid())
            .bind(fixture.context.agent_id.into_uuid())
            .execute(&fixture.state.pool)
            .await
            .unwrap();

    let stale_message = fixture
        .execute(capability::Action::MessageSend(capability::MessageSend {
            target: capability::MessageTarget::Focus,
            body: "must not commit".into(),
            handle_item_id: None,
            snapshot_sequence: None,
        }))
        .await
        .unwrap_err();
    assert_eq!(stale_message.code, capability::ErrorCode::ContextChanged);
    let stale_done = fixture
        .execute(capability::Action::TaskDone {
            result: "must not finish".into(),
            post_to: capability::PostTarget::Focus,
        })
        .await
        .unwrap_err();
    assert_eq!(stale_done.code, capability::ErrorCode::ContextChanged);
    let facts: (String, String, i64) = sqlx::query_as(
            "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM messages WHERE body_markdown IN ('must not commit','must not finish')) \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
        )
        .bind(task_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
    assert_eq!(facts, ("in_progress".into(), "running".into(), 0));
    fixture.destroy().await;
}

use axum::body::to_bytes;
use serde_json::Value;
use uuid::Uuid;

use super::*;

#[tokio::test]
async fn authentication_surfaces_cannot_be_substituted() {
    let error = write_context(
        AuthenticationSurface::Computer,
        AuthenticationSurface::Browser,
        Some(&Uuid::now_v7().to_string()),
    )
    .unwrap_err()
    .into_response();
    assert_eq!(error.status(), StatusCode::FORBIDDEN);

    let body: Value =
        serde_json::from_slice(&to_bytes(error.into_body(), usize::MAX).await.unwrap()).unwrap();
    assert_eq!(body["error"]["code"], "permission_denied");
    assert!(body["error"].get("details").is_none());
}

#[test]
fn every_write_requires_a_valid_idempotency_key() {
    assert!(
        write_context(
            AuthenticationSurface::Agent,
            AuthenticationSurface::Agent,
            None,
        )
        .is_err()
    );
    assert!(
        write_context(
            AuthenticationSurface::Agent,
            AuthenticationSurface::Agent,
            Some("not-a-uuid"),
        )
        .is_err()
    );
}

#[test]
fn task_create_dto_rejects_system_derived_source_fields() {
    let body = serde_json::json!({
        "title": "Task",
        "source_thread_id": Uuid::now_v7()
    });
    assert!(serde_json::from_value::<CreateTaskBody>(body).is_err());
}
