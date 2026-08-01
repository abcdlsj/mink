use super::*;

use std::str::FromStr;

use sqlx::{Connection, PgConnection, postgres::PgConnectOptions};
use time::OffsetDateTime;
use url::Url;

use crate::server::application::attention::{RequeueDeadItem, RequeueDeadItemInput};
use crate::server::application::conversation::{CreateAgentAction, CreateAgentActionInput};
use crate::server::application::execution::{
    ClaimRun, ClaimRunInput, ReclaimExpiredLeases, ReclaimExpiredLeasesInput,
};
use crate::server::application::ports::RawFencingToken;
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
        &in_readable_channel,
        viewer,
        false,
        &channels
    ));
    assert!(!event_is_visible(
        &in_private_channel,
        viewer,
        false,
        &channels
    ));
    // A governor reads Agent Inboxes but still holds no membership in a private Channel.
    assert!(!event_is_visible(
        &in_private_channel,
        viewer,
        true,
        &channels
    ));

    let own_inbox = json!({"member_id": viewer});
    let foreign_inbox = json!({"member_id": other_member});
    assert!(event_is_visible(&own_inbox, viewer, false, &channels));
    assert!(!event_is_visible(&foreign_inbox, viewer, false, &channels));
    assert!(event_is_visible(&foreign_inbox, viewer, true, &channels));

    // An event naming no Channel and no Member reaches every Space Member.
    assert!(event_is_visible(
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
        PostgresAdapter::new(pool.clone())
            .initialize_schema()
            .await
            .unwrap();

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
        let computer = Uuid::now_v7();
        let channel = Uuid::now_v7();
        let root = Uuid::now_v7();
        sqlx::raw_sql(&format!(
            "BEGIN;
             INSERT INTO spaces(id,slug,name,owner_member_id,created_at) VALUES ('{space}','mention-all','Mention All','{owner}',now());
             INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES
               ('{owner}','{space}','human','Owner','owner','owner',now()),
               ('{agent}','{space}','agent','Agent','agent','member',now()),
               ('{second_agent}','{space}','agent','Second','second','member',now());
             INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer}','{space}','Computer','localhost','linux','mention-all-hash','offline',1,now());
             INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES
               ('{agent}','{space}','{computer}','Act',1,'active','codex',now()),
               ('{second_agent}','{space}','{computer}','Act',1,'active','codex',now());
             INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
             INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES
               ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0),('{channel}','{space}','{second_agent}',now(),0);
             INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
             INSERT INTO threads(id,space_id,channel_id,root_message_id,created_at) VALUES ('{root}','{space}','{channel}','{root}',now());
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
                        expected_snapshot: None,
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
async fn expired_lease_reclaim_unblocks_the_agent_and_subscription_raises_thread_activity() {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_lease_reclaim_{}", Uuid::now_v7().simple());
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
            sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces (id,slug,name,owner_member_id,created_at) VALUES ('{space}','space','Space','{owner}',now());
                 INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner','owner',now());
                 INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{agent}','{space}','agent','Lin','lin','member',now());
                 INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{subscriber}','{space}','agent','Ada','ada','member',now());
                 INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','offline',1,now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent}','{space}','{computer_id}','Act',1,'active','codex',now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{subscriber}','{space}','{computer_id}','Watch',1,'active','codex',now());
                 INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
                 INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0),('{channel}','{space}','{subscriber}',now(),0);
                 INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
                 INSERT INTO threads (id,space_id,channel_id,root_message_id,created_at) VALUES ('{root}','{space}','{channel}','{root}',now());
                 INSERT INTO thread_subscriptions (thread_id,space_id,member_id,created_at) VALUES ('{root}','{space}','{subscriber}',now());
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,fencing_token_hash,lease_expires_at,created_at,started_at) VALUES ('{stale_run}','{space}','{agent}','{root}','running','hash',now()-interval '5 minutes',now(),now());
                 INSERT INTO inbox_items (id,space_id,agent_id,message_id,thread_id,kind,strength,status,available_at,lease_run_id,lease_expires_at,retry_count,created_at) VALUES ('{stale_item}','{space}','{agent}','{root}','{root}','mention','hard','leased',now(),'{stale_run}',now()-interval '5 minutes',0,now());
                 INSERT INTO run_items (run_id,inbox_item_id,delivery_seq,attached_at) VALUES ('{stale_run}','{stale_item}',1,now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();

            let reclaimed = ReclaimExpiredLeases::execute(
                &mut adapter,
                ReclaimExpiredLeasesInput {
                    now: OffsetDateTime::now_utc(),
                    limit: 10,
                    max_retry_count: 5,
                },
            )
            .await
            .unwrap();
            assert_eq!(reclaimed.runs_failed, 1);
            assert_eq!(reclaimed.items_released, 1);

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

            // The Run is terminal, so the partial unique index now admits a new Run for that Agent.
            sqlx::query(
                "INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,\
                 fencing_token_hash,lease_expires_at,created_at) \
                 VALUES ($1,$2,$3,$4,'queued','fresh',now()+interval '1 hour',now())",
            )
            .bind(Uuid::now_v7())
            .bind(space)
            .bind(agent)
            .bind(root)
            .execute(&pool)
            .await
            .expect("reclaiming the abandoned Run unblocks the Agent");

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
                            expected_snapshot: None,
                            now: OffsetDateTime::now_utc(),
                        })
                        .await
                })
                .await
                .unwrap();
            let routed: (String, String) = sqlx::query_as(
                "SELECT (SELECT kind FROM inbox_items WHERE agent_id=$1 ORDER BY created_at DESC LIMIT 1), \
                        (SELECT kind FROM inbox_items WHERE agent_id=$2 ORDER BY created_at DESC LIMIT 1)",
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
                 UPDATE inbox_items SET status='dead',lease_run_id=NULL,lease_expires_at=NULL,retry_count=5 WHERE id='{stale_item}';
                 INSERT INTO agent_runs (id,space_id,agent_id,focus_thread_id,status,fencing_token_hash,lease_expires_at,outcome_code,created_at,started_at,finished_at) VALUES ('{retired_run}','{space}','{agent}','{root}','completed','hash',now(),'completed',now(),now(),now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();
            // Terminate the queued Run inserted above so the Agent has capacity to claim again.
            sqlx::query(
                "UPDATE agent_runs SET status='canceled',outcome_code='canceled',finished_at=now() \
                 WHERE agent_id=$1 AND status='queued'",
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
            ClaimRun::execute(
                &mut adapter,
                ClaimRunInput {
                    run_id: RunId::from_uuid(Uuid::now_v7()),
                    agent_id: MemberId::from_uuid(agent),
                    computer_id: ComputerId::from_uuid(computer_id),
                    task_id: None,
                    focus_thread_id: ThreadId::from_uuid(root),
                    item_ids: vec![InboxItemId::from_uuid(stale_item)],
                    fencing_token: RawFencingToken::new("requeued".into()),
                    lease_expires_at: OffsetDateTime::now_utc() + time::Duration::minutes(2),
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
            sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces (id,slug,name,owner_member_id,created_at) VALUES ('{space}','space','Space','{owner}',now());
                 INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{owner}','{space}','human','Owner','owner','owner',now());
                 INSERT INTO members (id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{agent}','{space}','agent','Lin','lin','member',now());
                 INSERT INTO computers (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space}','Computer','localhost','linux','hash','offline',1,now());
                 INSERT INTO agents (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent}','{space}','{computer_id}','Act',1,'active','codex',now());
                 INSERT INTO channels (id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel}','{space}','public','general',2,now());
                 INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel}','{space}','{owner}',now(),0),('{channel}','{space}','{agent}',now(),0);
                 INSERT INTO messages (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{root}','{space}','{channel}','{root}',1,'root','text','{owner}','source',now());
                 INSERT INTO threads (id,space_id,channel_id,root_message_id,created_at) VALUES ('{root}','{space}','{channel}','{root}',now());
                 COMMIT;"
            ))
            .execute(&pool)
            .await
            .unwrap();

            // Twelve replies published concurrently. Each one is ordinary Channel activity for the
            // Agent, so all twelve belong in a single aggregate.
            const REPLIES: i32 = 12;
            let published = (0..REPLIES)
                .map(|_| {
                    let pool = pool.clone();
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
                                        thread_id: Some(ThreadId::from_uuid(root)),
                                        reply_to_message_id: None,
                                        mentions: Vec::new(),
                                        mention_all: false,
                                        attachment_ids: Vec::new(),
                                        handled_item: None,
                                        expected_snapshot: None,
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
                 FROM inbox_items WHERE agent_id=$1 LIMIT 1",
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
            // The Root Message holds sequence 1, so the replies occupy 2 through REPLIES + 1.
            assert_eq!((first_seq, last_seq), (2, i64::from(REPLIES) + 1));
            assert!(
                available_at <= force_at,
                "a busy Thread cannot postpone the aggregate past its deadline"
            );

            // The single-aggregate rule is enforced by the schema, not only by the write path.
            let duplicate = sqlx::query(
                "INSERT INTO inbox_items(id,space_id,agent_id,message_id,thread_id,task_id,kind,\
                 strength,status,available_at,first_message_seq,last_message_seq,aggregated_count,\
                 force_at,created_at) \
                 VALUES($1,$2,$3,NULL,$4,NULL,'channel_activity','ambient','pending',now(),99,99,1,\
                 now(),now())",
            )
            .bind(Uuid::now_v7())
            .bind(space)
            .bind(agent)
            .bind(root)
            .execute(&pool)
            .await;
            assert!(
                duplicate.is_err(),
                "a second open ambient aggregate for one Agent and Thread must be rejected"
            );

            // The aggregate names no source Message, so claiming it must not depend on one. Claiming
            // also closes it to further Messages, which is why this runs last.
            let aggregate_id: Uuid =
                sqlx::query_scalar("SELECT id FROM inbox_items WHERE agent_id=$1")
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
            ClaimRun::execute(
                &mut adapter,
                ClaimRunInput {
                    run_id: RunId::from_uuid(Uuid::now_v7()),
                    agent_id: MemberId::from_uuid(agent),
                    computer_id: ComputerId::from_uuid(computer_id),
                    task_id: None,
                    focus_thread_id: ThreadId::from_uuid(root),
                    item_ids: vec![InboxItemId::from_uuid(aggregate_id)],
                    fencing_token: RawFencingToken::new("token".into()),
                    lease_expires_at: OffsetDateTime::now_utc() + time::Duration::minutes(2),
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
            assert_eq!(leased, ("leased".into(), None));

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
