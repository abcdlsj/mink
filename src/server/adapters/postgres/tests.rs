use super::*;

use std::str::FromStr;

use sqlx::{Connection, PgConnection, postgres::PgConnectOptions};
use time::OffsetDateTime;
use url::Url;

use crate::server::application::attention::{RequeueDeadItem, RequeueDeadItemInput};
use crate::server::application::conversation::{CreateAgentAction, CreateAgentActionInput};
use crate::server::application::execution::{
    CompleteRun, CompleteRunInput, DispatchRun, DispatchRunInput, ItemDispositionInput,
    SyncComputerRuns, SyncComputerRunsInput,
};
use crate::server::application::identity::{AgentLifecycleAction, UpdateAgent, UpdateAgentInput};
use crate::server::application::ports::{InboxActivityEventKind, InboxScope};
use crate::server::application::task::{CreateTaskFromRootMessage, CreateTaskInput, TaskSource};

#[test]
fn the_event_stream_hides_unreadable_channels_and_other_members_inboxes() {
    let viewer = MemberId::from_uuid(Uuid::now_v7());
    let other_member = MemberId::from_uuid(Uuid::now_v7());
    let readable = Uuid::now_v7();
    let private = Uuid::now_v7();
    let channels = std::collections::HashSet::from([readable]);

    let in_readable_channel = json!({"resource_id": Uuid::now_v7(), "channel_id": readable});
    let in_private_channel = json!({"resource_id": Uuid::now_v7(), "channel_id": private});
    assert!(event_is_visible(
        "message.created",
        &in_readable_channel,
        viewer,
        false,
        &channels
    ));
    assert!(!event_is_visible(
        "message.created",
        &in_private_channel,
        viewer,
        false,
        &channels
    ));
    // A governor reads Agent Inboxes but still holds no membership in a private Channel.
    assert!(!event_is_visible(
        "message.created",
        &in_private_channel,
        viewer,
        true,
        &channels
    ));

    let own_inbox = json!({"member_id": viewer});
    let foreign_inbox = json!({"member_id": other_member});
    assert!(event_is_visible(
        "inbox.changed",
        &own_inbox,
        viewer,
        false,
        &channels
    ));
    assert!(!event_is_visible(
        "inbox.changed",
        &foreign_inbox,
        viewer,
        false,
        &channels
    ));
    assert!(event_is_visible(
        "inbox.changed",
        &foreign_inbox,
        viewer,
        true,
        &channels
    ));

    // agent.activity reaches readable scopes, but never crosses a Channel boundary.
    let agent_activity_in_readable = json!({"member_id": other_member, "channel_id": readable});
    let agent_activity_in_private = json!({"member_id": other_member, "channel_id": private});
    assert!(event_is_visible(
        "agent.activity",
        &agent_activity_in_readable,
        viewer,
        false,
        &channels
    ));
    assert!(!event_is_visible(
        "agent.activity",
        &agent_activity_in_private,
        viewer,
        false,
        &channels
    ));
    assert!(event_is_visible(
        "agent.activity",
        &json!({"member_id": other_member, "scope_channel_id": readable}),
        viewer,
        false,
        &channels
    ));
    assert!(!event_is_visible(
        "agent.activity",
        &json!({"member_id": other_member, "scope_channel_id": private}),
        viewer,
        false,
        &channels
    ));

    // An event naming no Channel and no Member reaches every Space Member.
    assert!(event_is_visible(
        "task.updated",
        &json!({"resource_id": Uuid::now_v7()}),
        viewer,
        false,
        &channels
    ));
}

#[tokio::test]
async fn empty_database_builds_final_schema_with_concurrency_constraints() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_server_adapter_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        let adapter = PostgresAdapter::new(pool.clone());
        adapter.initialize_schema().await.unwrap();

        let active_index: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM pg_indexes \
                 WHERE indexname='agent_runs_one_active_per_agent' \
                 AND indexdef LIKE '%WHERE (status <> ALL%')",
        )
        .fetch_one(&pool)
        .await
        .unwrap();
        let no_threads_table: bool = sqlx::query_scalar(
            "SELECT NOT EXISTS(SELECT 1 FROM information_schema.tables \
                 WHERE table_schema='public' AND table_name='threads')",
        )
        .fetch_one(&pool)
        .await
        .unwrap();
        let messages_self_fk: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM pg_constraint c \
                 JOIN pg_class r ON r.oid=c.conrelid \
                 JOIN pg_class f ON f.oid=c.confrelid \
                 WHERE r.relname='messages' AND f.relname='messages' AND c.contype='f')",
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
        let slug_constraint: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM pg_constraint \
                 WHERE conname='channels_slug_form_check')",
        )
        .fetch_one(&pool)
        .await
        .unwrap();
        let observed_thread_seq: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM information_schema.columns \
                 WHERE table_name='agent_runs' AND column_name='observed_thread_seq')",
        )
        .fetch_one(&pool)
        .await
        .unwrap();
        let schema_version: i32 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&pool)
            .await
            .unwrap();

        assert!(active_index);
        assert!(no_threads_table);
        assert!(messages_self_fk);
        assert!(!legacy_session_table);
        assert!(slug_constraint);
        assert!(observed_thread_seq);
        assert_eq!(schema_version, 9);

        sqlx::raw_sql(
            "ALTER TABLE channels DROP CONSTRAINT channels_slug_form_check; \
             DROP TABLE inbox_activity_events; \
             DROP INDEX inbox_items_open_channel_ambient_aggregate; \
             DROP INDEX inbox_items_open_thread_ambient_aggregate; \
             ALTER TABLE inbox_items DROP COLUMN ambient_channel_id; \
             ALTER TABLE agent_runs DROP COLUMN observed_thread_seq; \
             UPDATE schema_meta SET version=5 WHERE version=9;",
        )
        .execute(&pool)
        .await
        .unwrap();
        adapter.initialize_schema().await.unwrap();
        let migrated_version: i32 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&pool)
            .await
            .unwrap();
        let migrated_constraint: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM pg_constraint \
                 WHERE conname='channels_slug_form_check')",
        )
        .fetch_one(&pool)
        .await
        .unwrap();
        let migrated_observed_thread_seq: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM information_schema.columns \
                 WHERE table_name='agent_runs' AND column_name='observed_thread_seq')",
        )
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(migrated_version, 9);
        assert!(migrated_constraint);
        assert!(migrated_observed_thread_seq);
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
async fn channel_activity_query_rejects_another_computer_impersonating_an_agent() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_channel_query_auth_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        let adapter = PostgresAdapter::new(pool.clone());
        adapter.initialize_schema().await.unwrap();
        let space = Uuid::now_v7();
        let owner = Uuid::now_v7();
        let agent = Uuid::now_v7();
        let assigned_computer = Uuid::now_v7();
        let other_computer = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let root = Uuid::now_v7();
        let run = Uuid::now_v7();
        let inbox_item = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at)
                 VALUES ('{space}','query-auth','Query Auth','#F0602F','{owner}',now());
             INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES
                 ('{owner}','{space}','human','Owner','owner',now()),
                 ('{agent}','{space}','agent','Agent','member',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,created_at)
                 VALUES ('{assigned_computer}','{space}','Assigned','localhost','linux','assigned-hash','offline',now()),
                        ('{other_computer}','{space}','Other','localhost','linux','other-hash','offline',now());
             INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at)
                 VALUES ('{agent}','{space}','{assigned_computer}','Act',1,'active','builtin',now());
             INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at)
                 VALUES ('{channel}','{space}','public','general',2,now());
             INSERT INTO channel_members(channel_id,space_id,member_id,joined_at)
                 VALUES ('{channel}','{space}','{agent}',now());
             INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at)
                 VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{agent}','safe body',now());
             INSERT INTO agent_runs(id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at)
                 VALUES ('{run}','{space}','{agent}','{root}','dispatched','mention',now());
             INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,kind,strength,status,available_at,assigned_run_id,created_at)
                 VALUES ('{inbox_item}','{space}','{agent}','{root}','{root}','mention','hard','assigned',now(),'{run}',now());
             INSERT INTO run_items(run_id,inbox_item_id,delivery_seq,attached_at)
                 VALUES ('{run}','{inbox_item}',1,now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

        let result = adapter
            .channel_activity_snapshot(
                ComputerId::from_uuid(other_computer),
                RunId::from_uuid(run),
                MemberId::from_uuid(agent),
                ChannelId::from_uuid(channel),
                0,
                1,
                10,
            )
            .await;
        let error = result.err().expect("foreign computer must be rejected");
        assert_eq!(error, ApplicationError::PermissionDenied);
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
async fn run_item_diagnostic_columns_reject_partial_receipts_and_dispositions() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_run_item_constraints_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        let adapter = PostgresAdapter::new(pool.clone());
        adapter.initialize_schema().await.unwrap();
        let space = Uuid::now_v7();
        let owner = Uuid::now_v7();
        let agent = Uuid::now_v7();
        let computer = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let root = Uuid::now_v7();
        let run = Uuid::now_v7();
        let inbox_item = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at)
                 VALUES ('{space}','run-item-constraints','Run Item Constraints','#F0602F','{owner}',now());
             INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES
                 ('{owner}','{space}','human','Owner','owner',now()),
                 ('{agent}','{space}','agent','Agent','member',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,created_at)
                 VALUES ('{computer}','{space}','Computer','localhost','linux','computer-hash','offline',now());
             INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at)
                 VALUES ('{agent}','{space}','{computer}','Act',1,'active','builtin',now());
             INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at)
                 VALUES ('{channel}','{space}','public','general',2,now());
             INSERT INTO channel_members(channel_id,space_id,member_id,joined_at)
                 VALUES ('{channel}','{space}','{agent}',now());
             INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at)
                 VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{agent}','safe body',now());
             INSERT INTO agent_runs(id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at)
                 VALUES ('{run}','{space}','{agent}','{root}','dispatched','mention',now());
             INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,kind,strength,status,available_at,assigned_run_id,created_at)
                 VALUES ('{inbox_item}','{space}','{agent}','{root}','{root}','mention','hard','assigned',now(),'{run}',now());
             INSERT INTO run_items(run_id,inbox_item_id,delivery_seq,attached_at)
                 VALUES ('{run}','{inbox_item}',1,now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

        for assignment in [
            "delivery_outcome='accepted'".to_owned(),
            format!("delivery_event_id='{}'", Uuid::now_v7()),
            "delivery_receipt_at=now()".to_owned(),
            "disposition='handled'".to_owned(),
            "disposition_at=now()".to_owned(),
        ] {
            let update = format!(
                "UPDATE run_items SET {assignment} WHERE run_id='{run}' AND inbox_item_id='{inbox_item}'"
            );
            assert!(
                sqlx::query(&update).execute(&pool).await.is_err(),
                "invalid run item half-state must be rejected: {assignment}"
            );
        }
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
async fn mention_all_expands_active_channel_members_and_deduplicates_agents() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_mention_all_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        let mut adapter = PostgresAdapter::new(pool.clone());
        adapter.initialize_schema().await.unwrap();
        let space = Uuid::now_v7();
        let owner = Uuid::now_v7();
        let agent = Uuid::now_v7();
        let second_agent = Uuid::now_v7();
        let space_only = Uuid::now_v7();
        let computer = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let root = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','mention-all','Mention All','#F0602F','{owner}',now());
             INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES
               ('{owner}','{space}','human','Owner','owner',now()),
               ('{agent}','{space}','agent','Agent','member',now()),
               ('{second_agent}','{space}','agent','Second','member',now()),
               ('{space_only}','{space}','human','SpaceOnly','member',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer}','{space}','Computer','localhost','linux','mention-all-hash','offline',1,now());
             INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES
               ('{agent}','{space}','{computer}','Act',1,'active','codex',now()),
               ('{second_agent}','{space}','{computer}','Act',1,'active','codex',now());
             INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
             INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES
               ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0),('{channel}','{space}','{second_agent}',now(),0);
             INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();
        let message = MessageId::from_uuid(Uuid::now_v7());
        adapter
            .transact(async |transaction| {
                transaction
                    .publish_message(MessageDraft {
                        message_id: message,
                        channel_id: ChannelId::from_uuid(channel),
                        author_member_id: MemberId::from_uuid(owner),
                        idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
                        thread_id: Some(ThreadId::from_uuid(root)),
                        reply_to_message_id: None,
                        body_markdown: "@all @agent".into(),
                        mentions: vec![MemberId::from_uuid(agent), MemberId::from_uuid(agent)],
                        mention_all: true,
                        attachment_ids: Vec::new(),
                        handled_item: None,
                        now: OffsetDateTime::now_utc(),
                    })
                    .await
            })
            .await
            .unwrap();
        let facts: (i64, i64, bool) = sqlx::query_as(
            "SELECT (SELECT count(*) FROM message_mentions WHERE message_id=$1),
                    (SELECT count(*) FROM inbox_items WHERE message_id=$1 AND kind='mention'),
                    (SELECT mention_all FROM messages WHERE id=$1)",
        )
        .bind(message.into_uuid())
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(facts, (2, 2, true));
        let targets: Vec<Uuid> = sqlx::query_scalar(
            "SELECT member_id FROM message_mentions WHERE message_id=$1",
        )
        .bind(message.into_uuid())
        .fetch_all(&pool)
        .await
        .unwrap();
        assert!(!targets.contains(&owner));
        assert!(targets.contains(&agent));
        assert!(targets.contains(&second_agent));

        let run_id = RunId::from_uuid(Uuid::now_v7());
        sqlx::query(
            "INSERT INTO agent_runs(id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at) \
             VALUES($1,$2,$3,$4,'dispatched','mention',now())",
        )
        .bind(run_id.into_uuid())
        .bind(space)
        .bind(agent)
        .bind(root)
        .execute(&pool)
        .await
        .unwrap();
        let start = adapter
            .transact(async |transaction| transaction.run_start(run_id).await)
            .await
            .unwrap();
        let run_member_ids = start
            .channel_members
            .iter()
            .map(|member| member.member_id.into_uuid())
            .collect::<std::collections::BTreeSet<_>>();
        assert_eq!(run_member_ids.len(), 3);
        assert!(run_member_ids.contains(&owner));
        assert!(run_member_ids.contains(&agent));
        assert!(run_member_ids.contains(&second_agent));
        assert!(!run_member_ids.contains(&space_only));
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
async fn agent_messages_do_not_create_ambient_items_for_other_agents() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_agent_ambient_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        let mut adapter = PostgresAdapter::new(pool.clone());
        adapter.initialize_schema().await.unwrap();
        let space = Uuid::now_v7();
        let owner = Uuid::now_v7();
        let author_agent = Uuid::now_v7();
        let recipient_agent = Uuid::now_v7();
        let subscribed_agent = Uuid::now_v7();
        let computer = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let root = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','agent-ambient','Agent Ambient','#FE7DA8','{owner}',now());
             INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES
               ('{owner}','{space}','human','Owner','owner',now()),
               ('{author_agent}','{space}','agent','Author','member',now()),
               ('{recipient_agent}','{space}','agent','Recipient','member',now()),
               ('{subscribed_agent}','{space}','agent','Subscriber','member',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer}','{space}','Computer','localhost','linux','agent-ambient-hash','offline',1,now());
             INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES
               ('{author_agent}','{space}','{computer}','Act',1,'active','codex',now()),
               ('{recipient_agent}','{space}','{computer}','Act',1,'active','codex',now()),
               ('{subscribed_agent}','{space}','{computer}','Act',1,'active','codex',now());
             INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
             INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES
               ('{channel}','{space}','{owner}',now(),0),
               ('{channel}','{space}','{author_agent}',now(),0),
               ('{channel}','{space}','{recipient_agent}',now(),0),
               ('{channel}','{space}','{subscribed_agent}',now(),0);
             INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
             INSERT INTO thread_subscriptions(thread_id,space_id,member_id,created_at) VALUES ('{root}','{space}','{subscribed_agent}',now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

        let ordinary_message = MessageId::from_uuid(Uuid::now_v7());
        adapter
            .transact(async |transaction| {
                transaction
                    .publish_message(MessageDraft {
                        message_id: ordinary_message,
                        channel_id: ChannelId::from_uuid(channel),
                        author_member_id: MemberId::from_uuid(author_agent),
                        idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
                        body_markdown: "routine progress".into(),
                        thread_id: Some(ThreadId::from_uuid(root)),
                        reply_to_message_id: None,
                        mentions: Vec::new(),
                        mention_all: false,
                        attachment_ids: Vec::new(),
                        handled_item: None,
                        now: OffsetDateTime::now_utc(),
                    })
                    .await
            })
            .await
            .unwrap();
        let ordinary_items: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM inbox_items WHERE message_id=$1 OR (message_id IS NULL AND member_id IN ($2,$3))",
        )
        .bind(ordinary_message.into_uuid())
        .bind(recipient_agent)
        .bind(subscribed_agent)
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(ordinary_items, 0);

        let addressed_message = MessageId::from_uuid(Uuid::now_v7());
        adapter
            .transact(async |transaction| {
                transaction
                    .publish_message(MessageDraft {
                        message_id: addressed_message,
                        channel_id: ChannelId::from_uuid(channel),
                        author_member_id: MemberId::from_uuid(author_agent),
                        idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
                        body_markdown: "@Recipient please review".into(),
                        thread_id: Some(ThreadId::from_uuid(root)),
                        reply_to_message_id: None,
                        mentions: vec![MemberId::from_uuid(recipient_agent)],
                        mention_all: false,
                        attachment_ids: Vec::new(),
                        handled_item: None,
                        now: OffsetDateTime::now_utc(),
                    })
                    .await
            })
            .await
            .unwrap();
        let addressed: (Uuid, String, String) = sqlx::query_as(
            "SELECT member_id,kind,strength FROM inbox_items WHERE message_id=$1",
        )
        .bind(addressed_message.into_uuid())
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(addressed, (recipient_agent, "mention".into(), "hard".into()));
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
async fn failing_an_orphaned_run_unblocks_the_agent_and_subscription_raises_thread_activity() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_orphan_sync_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
            let mut adapter = PostgresAdapter::new(pool.clone());
            adapter.initialize_schema().await.unwrap();
            let space = Uuid::now_v7();
            let owner = Uuid::now_v7();
            let agent = Uuid::now_v7();
            let subscriber = Uuid::now_v7();
            let computer_id = Uuid::now_v7();
            let channel = Uuid::now_v7();
            let root = Uuid::now_v7();
            let stale_run = Uuid::now_v7();
            let stale_item = Uuid::now_v7();
            let pending_run = Uuid::now_v7();
            let pending_start_command = Uuid::now_v7();
            let stale_notice_command = Uuid::now_v7();
            let stale_notice_id = Uuid::now_v7();
            sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces (id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','space','Space','#F0602F','{owner}',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{agent}','{space}','agent','Lin','member',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{subscriber}','{space}','agent','Ada','member',now());
                 INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','offline',3,now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent}','{space}','{computer_id}','Act',1,'active','codex',now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{subscriber}','{space}','{computer_id}','Watch',1,'active','codex',now());
                 INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
                 INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0),('{channel}','{space}','{subscriber}',now(),0);
                 INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
                 INSERT INTO thread_subscriptions (thread_id,space_id,member_id,created_at) VALUES ('{root}','{space}','{subscriber}',now());
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at,started_at) VALUES ('{stale_run}','{space}','{agent}','{root}','working','mention',now(),now());
                 INSERT INTO inbox_items (id,space_id,member_id,message_id,thread_id,kind,strength,status,available_at,assigned_run_id,retry_count,created_at) VALUES ('{stale_item}','{space}','{agent}','{root}','{root}','mention','hard','assigned',now(),'{stale_run}',0,now());
                 INSERT INTO run_items (run_id,inbox_item_id,delivery_seq,attached_at) VALUES ('{stale_run}','{stale_item}',1,now());
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at) VALUES ('{pending_run}','{space}','{subscriber}','{root}','dispatched','mention',now());
                 INSERT INTO computer_commands (id,computer_id,computer_seq,kind,payload_json,created_at) VALUES ('{pending_start_command}','{computer_id}',1,'run.start',jsonb_build_object('kind','run.start','payload',jsonb_build_object('run_id','{pending_run}')),now());
                 INSERT INTO computer_commands (id,computer_id,computer_seq,kind,payload_json,created_at) VALUES ('{stale_notice_command}','{computer_id}',2,'run.notice',jsonb_build_object('kind','run.notice','payload',jsonb_build_object('run_id','{stale_run}','notice',jsonb_build_object('notice_id','{stale_notice_id}'))),now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();

            let reclaimed = SyncComputerRuns::execute(
                &mut adapter,
                SyncComputerRunsInput {
                    computer_id: ComputerId::from_uuid(computer_id),
                    // The Computer reconnected holding nothing, so the stale Run is gone with it.
                    live_run_ids: Vec::new(),
                    max_retry_count: 5,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .unwrap();
            assert_eq!(reclaimed.runs_failed, 1);
            assert_eq!(reclaimed.items_released, 1);

            // Commands targeting the failed orphaned Run are settled; the pending Run's start
            // command stays queued for the reconnecting Computer.
            let stale_notice_acked: bool = sqlx::query_scalar(
                "SELECT acked_at IS NOT NULL FROM computer_commands WHERE id=$1",
            )
            .bind(stale_notice_command)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert!(stale_notice_acked);
            let pending_start_acked: bool = sqlx::query_scalar(
                "SELECT acked_at IS NOT NULL FROM computer_commands WHERE id=$1",
            )
            .bind(pending_start_command)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert!(!pending_start_acked);

            let recovered: (String, String, String, i32) = sqlx::query_as(
                "SELECT r.status,r.outcome_code,i.status,i.retry_count \
                 FROM agent_runs r JOIN inbox_items i ON i.id=$2 WHERE r.id=$1",
            )
            .bind(stale_run)
            .bind(stale_item)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert_eq!(
                recovered,
                ("failed".into(), "failed".into(), "pending".into(), 1)
            );
            let pending_status: String = sqlx::query_scalar(
                "SELECT status FROM agent_runs WHERE id=$1",
            )
            .bind(pending_run)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert_eq!(pending_status, "dispatched");

            // The Run is terminal, so the partial unique index now admits a new Run for that Agent.
            sqlx::query(
                "INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,\
                 trigger_kind,created_at) \
                 VALUES ($1,$2,$3,$4,'dispatched','mention',now())",
            )
            .bind(Uuid::now_v7())
            .bind(space)
            .bind(agent)
            .bind(root)
            .execute(&pool)
            .await
            .expect("failing the abandoned Run unblocks the Agent");

            // A reply routes thread_activity to the subscriber and channel_activity to the rest.
            adapter
                .transact(async |transaction| {
                    transaction
                        .publish_message(MessageDraft {
                            message_id: MessageId::from_uuid(Uuid::now_v7()),
                            channel_id: ChannelId::from_uuid(channel),
                            author_member_id: MemberId::from_uuid(owner),
                            idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
                            body_markdown: "reply".into(),
                            thread_id: Some(ThreadId::from_uuid(root)),
                            reply_to_message_id: None,
                            mentions: Vec::new(),
                            mention_all: false,
                            attachment_ids: Vec::new(),
                            handled_item: None,
                            now: OffsetDateTime::now_utc(),
                        })
                        .await
                })
                .await
                .unwrap();
            let routed: (String, String) = sqlx::query_as(
                "SELECT (SELECT kind FROM inbox_items WHERE member_id=$1 ORDER BY created_at DESC LIMIT 1), \
                        (SELECT kind FROM inbox_items WHERE member_id=$2 ORDER BY created_at DESC LIMIT 1)",
            )
            .bind(subscriber)
            .bind(agent)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert_eq!(routed, ("thread_activity".into(), "channel_activity".into()));

            // Exhausting the retry budget retires the Item, and a governor's requeue must make it
            // claimable again rather than leaving it to be retired on the next expiry.
            let retired_run = Uuid::now_v7();
            sqlx::raw_sql(&format!(
                "BEGIN;
                 UPDATE inbox_items SET status='dead',assigned_run_id=NULL,retry_count=5 WHERE id='{stale_item}';
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,trigger_kind,outcome_code,created_at,started_at,finished_at) VALUES ('{retired_run}','{space}','{agent}','{root}','completed','mention','completed',now(),now(),now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();
            // Terminate the Run inserted above so the Agent has capacity to take work again.
            sqlx::query(
                "UPDATE agent_runs SET status='canceled',outcome_code='canceled',finished_at=now() \
                 WHERE agent_id=$1 AND status='dispatched'",
            )
            .bind(agent)
            .execute(&pool)
            .await
            .unwrap();

            let requeued = RequeueDeadItem::execute(
                &mut adapter,
                RequeueDeadItemInput {
                    item_id: InboxItemId::from_uuid(stale_item),
                    actor_id: MemberId::from_uuid(owner),
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .expect("the Space Owner returns a retired Item to the queue");
            assert_eq!(requeued.status, InboxItemStatus::Pending);
            assert_eq!((requeued.retry_count, requeued.requeue_count), (0, 1));

            // A fresh retry budget is what makes the requeue useful, so the Item must survive a claim.
            DispatchRun::execute(
                &mut adapter,
                DispatchRunInput {
                    run_id: RunId::from_uuid(Uuid::now_v7()),
                    agent_id: MemberId::from_uuid(agent),
                    task_id: None,
                    focus_thread_id: ThreadId::from_uuid(root),
                    trigger: RunTrigger::Mention,
                    item_ids: vec![InboxItemId::from_uuid(stale_item)],
                },
            )
            .await
            .expect("a requeued Item can be claimed again");

            // An Item already back in the queue is not a retirement to undo.
            assert!(
                RequeueDeadItem::execute(
                    &mut adapter,
                    RequeueDeadItemInput {
                        item_id: InboxItemId::from_uuid(stale_item),
                        actor_id: MemberId::from_uuid(owner),
                        now: OffsetDateTime::now_utc(),
                    },
                )
                .await
                .is_err()
            );

            let audited: i64 = sqlx::query_scalar(
                "SELECT count(*) FROM audit_events \
                 WHERE action='inbox_item.requeued' AND subject_type='inbox_item' AND subject_id=$1",
            )
            .bind(stale_item)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert_eq!(audited, 1, "only the accepted requeue is audited");

            pool.close().await;
        }
        .await;

    sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
        .execute(&mut admin)
        .await
        .unwrap();
    result
}

/// Terminal Run results settle queued commands targeting that Run, so a reconnect or a later
/// attention router cannot replay an obsolete start/attach/notice/stop to the Computer.
#[tokio::test]
async fn completing_a_run_settles_its_pending_commands() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_complete_settles_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
            let mut adapter = PostgresAdapter::new(pool.clone());
            adapter.initialize_schema().await.unwrap();
            let space = Uuid::now_v7();
            let owner = Uuid::now_v7();
            let agent = Uuid::now_v7();
            let computer_id = Uuid::now_v7();
            let channel = Uuid::now_v7();
            let root = Uuid::now_v7();
            let run_id = Uuid::now_v7();
            let item = Uuid::now_v7();
            let attach_command = Uuid::now_v7();
            let notice_command = Uuid::now_v7();
            let stop_command = Uuid::now_v7();
            let notice_id = Uuid::now_v7();
            sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces (id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','space','Space','#F0602F','{owner}',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{agent}','{space}','agent','Lin','member',now());
                 INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','online',4,now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent}','{space}','{computer_id}','Act',1,'active','codex',now());
                 INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
                 INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0);
                 INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at,started_at) VALUES ('{run_id}','{space}','{agent}','{root}','working','mention',now(),now());
                 INSERT INTO inbox_items (id,space_id,member_id,message_id,thread_id,kind,strength,status,available_at,assigned_run_id,retry_count,created_at) VALUES ('{item}','{space}','{agent}','{root}','{root}','mention','hard','assigned',now(),'{run_id}',0,now());
                 INSERT INTO run_items (run_id,inbox_item_id,delivery_seq,attached_at) VALUES ('{run_id}','{item}',1,now());
                 INSERT INTO computer_commands (id,computer_id,computer_seq,kind,payload_json,created_at) VALUES ('{attach_command}','{computer_id}',1,'run.attach_item',jsonb_build_object('kind','run.attach_item','payload',jsonb_build_object('run_id','{run_id}','delivery_sequence',2)),now());
                 INSERT INTO computer_commands (id,computer_id,computer_seq,kind,payload_json,created_at) VALUES ('{notice_command}','{computer_id}',2,'run.notice',jsonb_build_object('kind','run.notice','payload',jsonb_build_object('run_id','{run_id}','notice',jsonb_build_object('notice_id','{notice_id}'))),now());
                 INSERT INTO computer_commands (id,computer_id,computer_seq,kind,payload_json,created_at) VALUES ('{stop_command}','{computer_id}',3,'run.stop',jsonb_build_object('kind','run.stop','payload',jsonb_build_object('run_id','{run_id}')),now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();

            CompleteRun::execute(
                &mut adapter,
                CompleteRunInput {
                    event_id: EventId::from_uuid(Uuid::now_v7()),
                    run_id: RunId::from_uuid(run_id),
                    computer_id: ComputerId::from_uuid(computer_id),
                    outcome: RunOutcome::Completed,
                    error_code: None,
                    item_dispositions: vec![ItemDispositionInput {
                        item_id: InboxItemId::from_uuid(item),
                        disposition: InboxItemDisposition::Handled,
                    }],
                    continuation_note: None,
                    max_retry_count: 5,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .unwrap();

            let unacked: i64 = sqlx::query_scalar(
                "SELECT count(*) FROM computer_commands \
                 WHERE computer_id=$1 AND kind IN ('run.start','run.attach_item','run.notice','run.task_bound','run.stop') \
                   AND acked_at IS NULL",
            )
            .bind(computer_id)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert_eq!(unacked, 0, "terminal Run must not leave replayable commands");

            pool.close().await;
        }
        .await;

    sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
        .execute(&mut admin)
        .await
        .unwrap();
    result
}

/// Replay drops and settles commands queued for a Run that is already terminal. This closes the
/// race where the attention router queues a notice just as the Run result commits.
#[tokio::test]
async fn replay_skips_commands_for_terminal_runs() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_replay_stale_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
            let mut adapter = PostgresAdapter::new(pool.clone());
            adapter.initialize_schema().await.unwrap();
            let space = Uuid::now_v7();
            let owner = Uuid::now_v7();
            let agent = Uuid::now_v7();
            let computer_id = Uuid::now_v7();
            let channel = Uuid::now_v7();
            let root = Uuid::now_v7();
            let terminal_run = Uuid::now_v7();
            let live_run = Uuid::now_v7();
            sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces (id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','space','Space','#F0602F','{owner}',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{agent}','{space}','agent','Lin','member',now());
                 INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','offline',3,now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent}','{space}','{computer_id}','Act',1,'active','codex',now());
                 INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
                 INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0);
                 INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,trigger_kind,outcome_code,created_at,started_at,finished_at) VALUES ('{terminal_run}','{space}','{agent}','{root}','completed','mention','completed',now(),now(),now());
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at) VALUES ('{live_run}','{space}','{agent}','{root}','dispatched','mention',now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();
            adapter
                .transact(async |transaction| {
                    transaction
                        .queue_command(
                            ComputerId::from_uuid(computer_id),
                            Command::RunNotice(RunNotice {
                                run_id: RunId::from_uuid(terminal_run),
                                notice: AttentionNotice {
                                    notice_id: NoticeId::from_uuid(Uuid::now_v7()),
                                    source_kind: InboxSourceKind::Mention,
                                    strength: WireAttentionStrength::Hard,
                                    location: NoticeLocation::Visible {
                                        task_id: None,
                                        thread_id: ThreadId::from_uuid(root),
                                    },
                                    explicit_human_redirect: false,
                                    arrived_at: OffsetDateTime::now_utc(),
                                },
                            }),
                        )
                        .await?;
                    transaction
                        .queue_command(
                            ComputerId::from_uuid(computer_id),
                            Command::RunStop(RunStop {
                                run_id: RunId::from_uuid(live_run),
                                reason: StopReason::Suspend,
                            }),
                        )
                        .await?;
                    Ok(())
                })
                .await
                .unwrap();

            let replayed = crate::server::adapters::websocket::replay_commands(
                &adapter,
                ComputerId::from_uuid(computer_id),
                CommandSequence(0),
            )
            .await
            .unwrap();
            assert_eq!(replayed.len(), 1);
            let Command::RunStop(stop) = &replayed[0].command else {
                panic!("expected run.stop command")
            };
            assert_eq!(stop.run_id, RunId::from_uuid(live_run));
            assert_eq!(stop.reason, StopReason::Suspend);
            let pending: Vec<(String, Uuid)> = sqlx::query_as(
                "SELECT kind,(payload_json #>> '{payload,run_id}')::uuid \
                 FROM computer_commands WHERE computer_id=$1 AND acked_at IS NULL",
            )
            .bind(computer_id)
            .fetch_all(&pool)
            .await
            .unwrap();
            assert_eq!(
                pending,
                vec![("run.stop".to_owned(), live_run)],
                "the terminal Run's notice is settled; only the live Run's command replays"
            );

            pool.close().await;
        }
        .await;

    sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
        .execute(&mut admin)
        .await
        .unwrap();
    result
}

/// Concurrent ambient publishers must not lose count or force-time updates, and the schema must
/// refuse a second open aggregate for one Agent and Thread. Both are SQL-level guarantees, so this
/// runs against real PostgreSQL.
#[tokio::test]
async fn concurrent_ambient_messages_accumulate_into_one_bounded_aggregate() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_ambient_aggregate_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
            PostgresAdapter::new(pool.clone())
                .initialize_schema()
                .await
                .unwrap();
            let space = Uuid::now_v7();
            let owner = Uuid::now_v7();
            let agent = Uuid::now_v7();
            let computer_id = Uuid::now_v7();
            let channel = Uuid::now_v7();
            let root = Uuid::now_v7();
            let second_root = Uuid::now_v7();
            sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces (id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','space','Space','#F0602F','{owner}',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner',now());
                 INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{agent}','{space}','agent','Lin','member',now());
                 INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','offline',1,now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent}','{space}','{computer_id}','Act',1,'active','codex',now());
                 INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',3,now());
                 INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0);
                 INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
                 INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{second_root}','{space}','{channel}','{second_root}',2,'root','text','{owner}','second source',now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();

            // Twelve replies published concurrently. Each one is ordinary Channel activity for the
            // Agent, so all twelve belong in a single aggregate.
            const REPLIES: i32 = 12;
            let published = (0..REPLIES)
                .map(|index| {
                    let pool = pool.clone();
                    let thread_id = if index < REPLIES / 2 { root } else { second_root };
                    tokio::spawn(async move {
                        let mut adapter = PostgresAdapter::new(pool);
                        adapter
                            .transact(async |transaction| {
                                transaction
                                    .publish_message(MessageDraft {
                                        message_id: MessageId::from_uuid(Uuid::now_v7()),
                                        channel_id: ChannelId::from_uuid(channel),
                                        author_member_id: MemberId::from_uuid(owner),
                                        idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
                                        body_markdown: "reply".into(),
                                        thread_id: Some(ThreadId::from_uuid(thread_id)),
                                        reply_to_message_id: None,
                                        mentions: Vec::new(),
                                        mention_all: false,
                                        attachment_ids: Vec::new(),
                                        handled_item: None,
                                        now: OffsetDateTime::now_utc(),
                                    })
                                    .await
                            })
                            .await
                    })
                })
                .collect::<Vec<_>>();
            for task in published {
                task.await
                    .expect("the publisher task runs")
                    .expect("every concurrent ambient publisher commits");
            }

            let (items, count, first_seq, last_seq, force_at, available_at): (
                i64,
                i32,
                i64,
                i64,
                OffsetDateTime,
                OffsetDateTime,
            ) = sqlx::query_as(
                "SELECT count(*) OVER (),aggregated_count,first_message_seq,last_message_seq,\
                        force_at,available_at \
                 FROM inbox_items WHERE member_id=$1 LIMIT 1",
            )
            .bind(agent)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert_eq!(items, 1, "ambient activity collapses into one Item");
            assert_eq!(
                count, REPLIES,
                "no concurrent update was lost from the count"
            );
            // The two Root Messages hold sequences 1 and 2, so replies from both Threads occupy
            // one Channel aggregate over sequences 3 through REPLIES + 2.
            assert_eq!((first_seq, last_seq), (3, i64::from(REPLIES) + 2));
            let event_count: i64 = sqlx::query_scalar(
                "SELECT count(*) FROM inbox_activity_events e JOIN inbox_items i ON i.id=e.inbox_item_id WHERE i.member_id=$1",
            )
            .bind(agent)
            .fetch_one(&pool)
            .await
            .unwrap();
            assert_eq!(event_count, i64::from(REPLIES));
            assert!(
                available_at <= force_at,
                "a busy Thread cannot postpone the aggregate past its deadline"
            );

            // The single-aggregate rule is enforced by the schema, not only by the write path.
            let duplicate = sqlx::query(
                "INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,task_id,kind,\
                 strength,status,available_at,first_message_seq,last_message_seq,aggregated_count,\
                 force_at,ambient_channel_id,created_at) \
                 VALUES($1,$2,$3,NULL,$4,NULL,'channel_activity','ambient','pending',now(),99,99,1,\
                 now(),$5,now())",
            )
            .bind(Uuid::now_v7())
            .bind(space)
            .bind(agent)
            .bind(root)
            .bind(channel)
            .execute(&pool)
            .await;
            assert!(
                duplicate.is_err(),
                "a second open ambient aggregate for one Agent and Channel must be rejected"
            );

            // The aggregate names no source Message, so claiming it must not depend on one. Claiming
            // also closes it to further Messages, which is why this runs last.
            let (aggregate_id, aggregate_thread): (Uuid, Uuid) = sqlx::query_as(
                "SELECT id,thread_id FROM inbox_items WHERE member_id=$1",
            )
            .bind(agent)
            .fetch_one(&pool)
            .await
            .unwrap();
            sqlx::query("UPDATE inbox_items SET available_at=now() WHERE id=$1")
                .bind(aggregate_id)
                .execute(&pool)
                .await
                .unwrap();
            let mut adapter = PostgresAdapter::new(pool.clone());
            DispatchRun::execute(
                &mut adapter,
                DispatchRunInput {
                    run_id: RunId::from_uuid(Uuid::now_v7()),
                    agent_id: MemberId::from_uuid(agent),
                    task_id: None,
                    focus_thread_id: ThreadId::from_uuid(aggregate_thread),
                    trigger: RunTrigger::Mention,
                    item_ids: vec![InboxItemId::from_uuid(aggregate_id)],
                },
            )
            .await
            .expect("an ambient aggregate is claimable without a source Message");
            let leased: (String, Option<Uuid>) =
                sqlx::query_as("SELECT status,message_id FROM inbox_items WHERE id=$1")
                    .bind(aggregate_id)
                    .fetch_one(&pool)
                    .await
                    .unwrap();
            assert_eq!(leased, ("assigned".into(), None));

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
async fn channel_member_activity_preserves_order_and_stops_after_leave() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_member_activity_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        PostgresAdapter::new(pool.clone())
            .initialize_schema()
            .await
            .unwrap();
        let space = Uuid::now_v7();
        let owner = Uuid::now_v7();
        let watcher = Uuid::now_v7();
        let joining_agent = Uuid::now_v7();
        let computer_id = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let root = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces (id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','space','Space','#F0602F','{owner}',now());
             INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner',now());
             INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{watcher}','{space}','agent','Watcher','member',now());
             INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{joining_agent}','{space}','agent','Joining','member',now());
             INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','offline',1,now());
             INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{watcher}','{space}','{computer_id}','Watch',1,'active','codex',now()),('{joining_agent}','{space}','{computer_id}','Join',1,'active','codex',now());
             INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
             INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{watcher}',now(),0);
             INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

        let mut adapter = PostgresAdapter::new(pool.clone());
        let inserted = adapter
            .transact(async |transaction| {
                transaction
                    .add_channel_agents(
                        MemberId::from_uuid(owner),
                        ChannelId::from_uuid(channel),
                        vec![MemberId::from_uuid(joining_agent)],
                        IdempotencyKey::from_uuid(Uuid::now_v7()),
                        OffsetDateTime::now_utc(),
                    )
                    .await
            })
            .await
            .unwrap();
        assert_eq!(inserted, vec![MemberId::from_uuid(joining_agent)]);

        let first_message = MessageId::from_uuid(Uuid::now_v7());
        adapter
            .transact(async |transaction| {
                transaction
                    .publish_message(MessageDraft {
                        message_id: first_message,
                        channel_id: ChannelId::from_uuid(channel),
                        author_member_id: MemberId::from_uuid(owner),
                        idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
                        body_markdown: "first message".into(),
                        thread_id: Some(ThreadId::from_uuid(root)),
                        reply_to_message_id: None,
                        mentions: Vec::new(),
                        mention_all: false,
                        attachment_ids: Vec::new(),
                        handled_item: None,
                        now: OffsetDateTime::now_utc(),
                    })
                    .await
            })
            .await
            .unwrap();

        adapter
            .transact(async |transaction| {
                transaction
                    .remove_channel_agent(
                        MemberId::from_uuid(owner),
                        ChannelId::from_uuid(channel),
                        MemberId::from_uuid(joining_agent),
                        OffsetDateTime::now_utc(),
                    )
                    .await
            })
            .await
            .unwrap();

        adapter
            .transact(async |transaction| {
                transaction
                    .publish_message(MessageDraft {
                        message_id: MessageId::from_uuid(Uuid::now_v7()),
                        channel_id: ChannelId::from_uuid(channel),
                        author_member_id: MemberId::from_uuid(owner),
                        idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
                        body_markdown: "after leave".into(),
                        thread_id: Some(ThreadId::from_uuid(root)),
                        reply_to_message_id: None,
                        mentions: Vec::new(),
                        mention_all: false,
                        attachment_ids: Vec::new(),
                        handled_item: None,
                        now: OffsetDateTime::now_utc(),
                    })
                    .await
            })
            .await
            .unwrap();

        let watcher_events: Vec<(i64, String)> = sqlx::query_as(
            "SELECT e.channel_seq,e.kind FROM inbox_activity_events e \
             JOIN inbox_items i ON i.id=e.inbox_item_id \
             WHERE i.member_id=$1 ORDER BY e.channel_seq",
        )
        .bind(watcher)
        .fetch_all(&pool)
        .await
        .unwrap();
        assert_eq!(
            watcher_events,
            vec![
                (2, "member_joined".to_owned()),
                (3, "message".to_owned()),
                (4, "member_left".to_owned()),
                (5, "message".to_owned()),
            ]
        );

        let watcher_items = adapter
            .transact(async |transaction| {
                transaction
                    .inbox_for_member(MemberId::from_uuid(watcher), InboxScope::Queue)
                    .await
            })
            .await
            .unwrap();
        assert_eq!(watcher_items.len(), 1);
        assert_eq!(
            watcher_items[0]
                .activity_events
                .iter()
                .map(|event| (event.sequence, event.kind))
                .collect::<Vec<_>>(),
            vec![
                (2, InboxActivityEventKind::MemberJoined),
                (3, InboxActivityEventKind::Message),
                (4, InboxActivityEventKind::MemberLeft),
                (5, InboxActivityEventKind::Message),
            ]
        );

        let joining_counts: (i64, i64) = sqlx::query_as(
            "SELECT count(DISTINCT i.id),count(e.channel_seq) \
             FROM inbox_items i LEFT JOIN inbox_activity_events e ON e.inbox_item_id=i.id \
             WHERE i.member_id=$1",
        )
        .bind(joining_agent)
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(joining_counts, (1, 1));

        let candidates = adapter
            .transact(async |transaction| {
                transaction
                    .dispatchable_work(
                        OffsetDateTime::now_utc() + time::Duration::minutes(1),
                        64,
                    )
                    .await
            })
            .await
            .unwrap();
        assert!(!candidates
            .iter()
            .any(|candidate| candidate.agent_id == MemberId::from_uuid(joining_agent)));

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
async fn v6_to_v7_migration_merges_channel_aggregates_across_threads() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_activity_migration_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        let adapter = PostgresAdapter::new(pool.clone());
        adapter.initialize_schema().await.unwrap();
        let space = Uuid::now_v7();
        let owner = Uuid::now_v7();
        let agent = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let first_root = Uuid::now_v7();
        let second_root = Uuid::now_v7();
        let first_item = Uuid::now_v7();
        let second_item = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             DROP TABLE inbox_activity_events;
             DROP INDEX inbox_items_open_channel_ambient_aggregate;
             DROP INDEX inbox_items_open_thread_ambient_aggregate;
             ALTER TABLE inbox_items DROP COLUMN ambient_channel_id;
             ALTER TABLE agent_runs DROP COLUMN observed_thread_seq;
             CREATE UNIQUE INDEX inbox_items_open_ambient_aggregate ON inbox_items(member_id,thread_id) WHERE strength='ambient' AND status='pending' AND retry_count=0;
             UPDATE schema_meta SET version=6 WHERE version=9;
             INSERT INTO spaces (id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','migration-space','Migration Space','#F0602F','{owner}',now());
             INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner',now()),('{agent}','{space}','agent','Migrator','member',now());
             INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','migration',5,now());
             INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{first_root}','{space}','{channel}','{first_root}',1,'root','text','{owner}','first',now()),('{second_root}','{space}','{channel}','{second_root}',2,'root','text','{owner}','second',now());
             INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,task_id,kind,strength,status,available_at,retry_count,first_message_seq,last_message_seq,aggregated_count,force_at,created_at) VALUES
                ('{first_item}','{space}','{agent}',NULL,'{first_root}',NULL,'channel_activity','ambient','pending',now(),0,3,3,1,now(),now()),
                ('{second_item}','{space}','{agent}',NULL,'{second_root}',NULL,'channel_activity','ambient','pending',now(),0,2,4,2,now()+interval '10 minutes',now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

        adapter.initialize_schema().await.unwrap();
        let merged: (i64, i32, i64, i64, Uuid) = sqlx::query_as(
            "SELECT count(*),aggregated_count,first_message_seq,last_message_seq,ambient_channel_id \
             FROM inbox_items WHERE member_id=$1 GROUP BY aggregated_count,first_message_seq,last_message_seq,ambient_channel_id",
        )
        .bind(agent)
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(merged, (1, 3, 2, 4, channel));
        assert_eq!(
            sqlx::query_scalar::<_, i32>("SELECT max(version) FROM schema_meta")
                .fetch_one(&pool)
                .await
                .unwrap(),
            9
        );
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
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
    adapter.initialize_schema().await.unwrap();
    let space = Uuid::now_v7();
    let member = Uuid::now_v7();
    let channel = Uuid::now_v7();
    let root = Uuid::now_v7();
    let actor_agent = Uuid::now_v7();
    let computer_id = Uuid::now_v7();
    let run_id = Uuid::now_v7();
    sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces (id,slug,name,accent,owner_member_id,created_at) VALUES ('{space}','space','Space','#F0602F','{member}',now());
             INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{member}','{space}','human','Owner','owner',now());
             INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) VALUES ('{actor_agent}','{space}','agent','Actor','member',now());
             INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','online',1,now());
             INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{actor_agent}','{space}','{computer_id}','Act',1,'active','codex',now());
             INSERT INTO member_permissions (member_id,space_id,action_code,granted_by_member_id,created_at) VALUES ('{actor_agent}','{space}','agent.create','{member}',now());
             INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','private','general',2,now());
             INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{member}',now(),0);
             INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{actor_agent}',now(),0);
             INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{member}','source',now());
             INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,trigger_kind,created_at,started_at) VALUES ('{run_id}','{space}','{actor_agent}','{root}','working','mention',now(),now());
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
            source_thread_id: None,
            link_thread_ids: vec![],
            idempotency_key,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .unwrap();
    assert_eq!(created.view().id, task_id);
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

    let new_root = Uuid::now_v7();
    sqlx::raw_sql(&format!(
        "INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement, \
         content_kind,author_member_id,body_markdown,created_at) \
         VALUES ('{new_root}','{space}','{channel}','{new_root}',2,'root','text', \
                 '{actor_agent}','independent',now());"
    ))
    .execute(&pool)
    .await
    .unwrap();
    let second_task_id = TaskId::from_uuid(Uuid::now_v7());
    let created = CreateTaskFromRootMessage::execute(
        &mut adapter,
        CreateTaskInput {
            task_id: second_task_id,
            actor_member_id: MemberId::from_uuid(actor_agent),
            source: TaskSource::AgentRun(RunId::from_uuid(run_id)),
            title: "独立任务".into(),
            assignee_agent_member_id: None,
            source_thread_id: Some(ThreadId::from_uuid(new_root)),
            link_thread_ids: vec![],
            idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .unwrap();
    assert_eq!(created.view().status, TaskStatus::Todo);
    let item: (Uuid, Uuid, String, String) =
        sqlx::query_as("SELECT thread_id,task_id,kind,status FROM inbox_items WHERE member_id=$1")
            .bind(actor_agent)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(
        item,
        (
            new_root,
            second_task_id.into_uuid(),
            "task_activity".to_owned(),
            "pending".to_owned()
        )
    );

    let conflicting_root = Uuid::now_v7();
    sqlx::raw_sql(&format!(
        "INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement, \
         content_kind,author_member_id,body_markdown,created_at) \
         VALUES ('{conflicting_root}','{space}','{channel}','{conflicting_root}',3,'root', \
                 'text','{actor_agent}','conflict',now());"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query("UPDATE channels SET next_seq=4 WHERE id=$1")
        .bind(channel)
        .execute(&pool)
        .await
        .unwrap();
    let conflict = CreateTaskFromRootMessage::execute(
        &mut adapter,
        CreateTaskInput {
            task_id: TaskId::from_uuid(Uuid::now_v7()),
            actor_member_id: MemberId::from_uuid(actor_agent),
            source: TaskSource::AgentRun(RunId::from_uuid(run_id)),
            title: "冲突任务".into(),
            assignee_agent_member_id: None,
            source_thread_id: Some(ThreadId::from_uuid(conflicting_root)),
            link_thread_ids: vec![ThreadId::from_uuid(root)],
            idempotency_key: IdempotencyKey::from_uuid(Uuid::now_v7()),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .unwrap_err();
    assert_eq!(conflict, ApplicationError::Conflict);
    let item_count: i64 = sqlx::query_scalar("SELECT count(*) FROM inbox_items WHERE member_id=$1")
        .bind(actor_agent)
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(item_count, 1);

    let invalid_done = sqlx::query("UPDATE tasks SET status='done',finished_at=now() WHERE id=$1")
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
             trigger_kind,created_at) \
             VALUES ($1,$2,$3,$4,'dispatched','mention',now())",
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
            display_name: "NewAgent".into(),
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
        sqlx::query_scalar("SELECT count(*) FROM computer_commands WHERE kind='agent.provision'")
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
async fn committed_command_wakes_the_online_connection() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_command_wakeup_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
        let mut adapter = PostgresAdapter::new(pool.clone());
        adapter.initialize_schema().await.unwrap();
        let space_id = Uuid::now_v7();
        let owner_id = Uuid::now_v7();
        let computer_id = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at) VALUES ('{space_id}','wakeup','Wake Up','#F0602F','{owner_id}',now());
             INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner_id}','{space_id}','human','Owner','owner',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space_id}','Computer','localhost','linux','wakeup-hash','online',1,now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();
        let registry = adapter.commands();
        let (_connection, mut wakeups) = registry.connect(computer_id);

        adapter
            .transact(async |transaction| {
                transaction
                    .queue_command(
                        ComputerId::from_uuid(computer_id),
                        Command::AgentRetire(AgentRetire {
                            agent_id: AgentId::from_uuid(Uuid::now_v7()),
                        }),
                    )
                    .await
            })
            .await
            .unwrap();

        assert_eq!(
            tokio::time::timeout(std::time::Duration::from_secs(1), wakeups.recv())
                .await
                .expect("committed command must wake the online connection")
                .expect("command registry channel must stay open"),
            ()
        );

        let pending: i64 =
            sqlx::query_scalar("SELECT count(*) FROM computer_commands WHERE acked_at IS NULL")
                .fetch_one(&pool)
                .await
                .unwrap();
        assert_eq!(pending, 1);
    }
    .await;

    sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
        .execute(&mut admin)
        .await
        .unwrap();
    result
}

#[tokio::test]
async fn claim_run_inserts_the_run_before_leasing_its_inbox_item() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_claim_run_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
    adapter.initialize_schema().await.unwrap();
    let space_id = Uuid::now_v7();
    let owner_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let computer_id = Uuid::now_v7();
    let channel_id = Uuid::now_v7();
    let message_id = Uuid::now_v7();
    let item_id = Uuid::now_v7();
    sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at) VALUES ('{space_id}','claim-run','Claim Run','#F0602F','{owner_id}',now());
             INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner_id}','{space_id}','human','Owner','owner',now());
             INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES ('{agent_id}','{space_id}','agent','Agent','member',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space_id}','Computer','localhost','linux','claim-run-hash','online',1,now());
             INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent_id}','{space_id}','{computer_id}','Reply',1,'active','codex',now());
             INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel_id}','{space_id}','public','general',2,now());
             INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel_id}','{space_id}','{owner_id}',now(),0),('{channel_id}','{space_id}','{agent_id}',now(),0);
             INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{message_id}','{space_id}','{channel_id}','{message_id}',1,'root','text','{owner_id}','mention',now());
             INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,kind,strength,status,available_at,last_error_code,created_at) VALUES ('{item_id}','{space_id}','{agent_id}','{message_id}','{message_id}','mention','hard','pending',now(),'run_claim_unavailable',now());
             COMMIT;"
        ))
        .execute(&pool)
        .await
        .unwrap();

    let run_id = RunId::from_uuid(Uuid::now_v7());
    DispatchRun::execute(
        &mut adapter,
        DispatchRunInput {
            run_id,
            agent_id: MemberId::from_uuid(agent_id),
            task_id: None,
            focus_thread_id: ThreadId::from_uuid(message_id),
            trigger: RunTrigger::Mention,
            item_ids: vec![InboxItemId::from_uuid(item_id)],
        },
    )
    .await
    .unwrap();

    let facts: (String, String, Option<String>, i64, i64, i64) = sqlx::query_as(
        "SELECT r.status,i.status,i.last_error_code, \
             (SELECT count(*) FROM run_items WHERE run_id=r.id), \
             (SELECT count(*) FROM computer_commands WHERE kind='run.start'), \
             (SELECT count(*) FROM outbox_events WHERE kind='message.updated') \
             FROM agent_runs r JOIN inbox_items i ON i.assigned_run_id=r.id WHERE r.id=$1",
    )
    .bind(run_id.into_uuid())
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        facts,
        ("dispatched".into(), "assigned".into(), None, 1, 1, 1)
    );

    pool.close().await;
    sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
        .execute(&mut admin)
        .await
        .unwrap();
}

#[tokio::test]
async fn resuming_an_agent_queues_resume_before_a_later_run_start() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_resume_order_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
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
    adapter.initialize_schema().await.unwrap();
    let space_id = Uuid::now_v7();
    let owner_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let computer_id = Uuid::now_v7();
    let message_id = Uuid::now_v7();
    let item_id = Uuid::now_v7();
    sqlx::raw_sql(&format!(
        "BEGIN;
         INSERT INTO spaces(id,slug,name,accent,owner_member_id,created_at) VALUES ('{space_id}','resume-order','Resume Order','#F0602F','{owner_id}',now());
         INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES ('{owner_id}','{space_id}','human','Owner','owner',now());
         INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES ('{agent_id}','{space_id}','agent','Agent','member',now());
         INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space_id}','Computer','localhost','linux','resume-order-hash','offline',1,now());
         INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent_id}','{space_id}','{computer_id}','Reply',1,'suspended','codex',now());
         INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{message_id}','{space_id}','public','general',2,now());
         INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{message_id}','{space_id}','{owner_id}',now(),0),('{message_id}','{space_id}','{agent_id}',now(),0);
         INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{message_id}','{space_id}','{message_id}','{message_id}',1,'root','text','{owner_id}','mention',now());
         INSERT INTO inbox_items(id,space_id,member_id,message_id,thread_id,kind,strength,status,available_at,created_at) VALUES ('{item_id}','{space_id}','{agent_id}','{message_id}','{message_id}','mention','hard','pending',now(),now());
         COMMIT;"
    ))
    .execute(&pool)
    .await
    .unwrap();

    UpdateAgent::execute(
        &mut adapter,
        UpdateAgentInput {
            actor_id: MemberId::from_uuid(owner_id),
            agent_id: MemberId::from_uuid(agent_id),
            role_text: None,
            lifecycle: Some(AgentLifecycleAction::Resume),
        },
    )
    .await
    .unwrap();

    DispatchRun::execute(
        &mut adapter,
        DispatchRunInput {
            run_id: RunId::from_uuid(Uuid::now_v7()),
            agent_id: MemberId::from_uuid(agent_id),
            task_id: None,
            focus_thread_id: ThreadId::from_uuid(message_id),
            trigger: RunTrigger::Mention,
            item_ids: vec![InboxItemId::from_uuid(item_id)],
        },
    )
    .await
    .unwrap();

    let commands: Vec<(i64, String)> = sqlx::query_as(
        "SELECT computer_seq,kind FROM computer_commands WHERE computer_id=$1 ORDER BY computer_seq",
    )
    .bind(computer_id)
    .fetch_all(&pool)
    .await
    .unwrap();
    assert_eq!(
        commands,
        vec![(1, "agent.resume".to_owned()), (2, "run.start".to_owned())]
    );

    pool.close().await;
    sqlx::query(&format!("DROP DATABASE \"{database_name}\" WITH (FORCE)"))
        .execute(&mut admin)
        .await
        .unwrap();
}
