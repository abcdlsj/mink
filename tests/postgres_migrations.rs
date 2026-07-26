use std::str::FromStr;

use anyhow::{Context, Result};
use sqlx::{Connection, Executor, PgConnection, postgres::PgConnectOptions};
use url::Url;
use uuid::Uuid;

#[tokio::test]
async fn postgres_migrations_create_phase_one_schema() -> Result<()> {
    let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
    let database_name = format!("sumi_test_{}", Uuid::now_v7().simple());
    let mut admin = PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url)?)
        .await
        .context("connect to PostgreSQL test administrator database")?;

    admin
        .execute(format!("CREATE DATABASE \"{database_name}\"").as_str())
        .await
        .context("create isolated PostgreSQL test database")?;

    let test_result = run_migration_assertions(&admin_url, &database_name).await;

    admin
        .execute(format!("DROP DATABASE \"{database_name}\" WITH (FORCE)").as_str())
        .await
        .context("drop isolated PostgreSQL test database")?;

    test_result
}

async fn run_migration_assertions(admin_url: &str, database_name: &str) -> Result<()> {
    let mut url = Url::parse(admin_url)?;
    url.set_path(&format!("/{database_name}"));
    let pool = sumi_database_for_test(url.as_str()).await?;

    let table_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM information_schema.tables \
         WHERE table_schema = 'public' AND table_name IN \
         ('users', 'sessions', 'spaces', 'members', 'human_members', \
          'member_permissions', 'human_invitations', 'channels', 'channel_members', \
          'messages', 'message_mentions', 'inbox_items', \
          'threads', 'thread_subscriptions', \
          'direct_channels', 'attachments', 'message_attachments', \
          'audit_events', 'outbox_events', 'idempotency_records',
          'computers', 'computer_pairings', 'computer_commands', 'agents',
          'agent_memory_files', 'agent_runs', 'agent_run_inbox_items')",
    )
    .fetch_one(&pool)
    .await?;
    assert_eq!(table_count, 27);

    let owner_constraint_is_deferred: bool = sqlx::query_scalar(
        "SELECT condeferrable AND condeferred FROM pg_constraint \
         WHERE conname = 'spaces_owner_member_in_space'",
    )
    .fetch_one(&pool)
    .await?;
    assert!(owner_constraint_is_deferred);

    let invitation_member_is_space_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conname = 'human_invitations_acceptor_in_space')",
    )
    .fetch_one(&pool)
    .await?;
    assert!(invitation_member_is_space_scoped);

    let permission_granter_is_space_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conname = 'member_permissions_granter_in_space')",
    )
    .fetch_one(&pool)
    .await?;
    assert!(permission_granter_is_space_scoped);

    let message_author_is_space_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conrelid = 'messages'::regclass AND contype = 'f' \
           AND array_length(conkey, 1) = 2)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(message_author_is_space_scoped);

    let mention_requires_channel_membership: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conrelid = 'message_mentions'::regclass \
           AND confrelid = 'channel_members'::regclass)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(mention_requires_channel_membership);

    let thread_root_is_channel_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conrelid = 'threads'::regclass AND confrelid = 'messages'::regclass \
           AND array_length(conkey, 1) = 3)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(thread_root_is_channel_scoped);

    let direct_pair_has_two_deferred_memberships: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM pg_constraint \
         WHERE conrelid = 'direct_channels'::regclass \
           AND confrelid = 'channel_members'::regclass \
           AND condeferrable AND condeferred",
    )
    .fetch_one(&pool)
    .await?;
    assert_eq!(direct_pair_has_two_deferred_memberships, 2);

    let attachment_message_is_space_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conrelid = 'message_attachments'::regclass \
           AND confrelid = 'messages'::regclass AND array_length(conkey, 1) = 3)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(attachment_message_is_space_scoped);

    let pairing_confirmer_is_space_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conrelid = 'computer_pairings'::regclass \
           AND confrelid = 'members'::regclass AND array_length(conkey, 1) = 2)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(pairing_confirmer_is_space_scoped);

    let pairing_computer_is_space_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint \
         WHERE conrelid = 'computer_pairings'::regclass \
           AND confrelid = 'computers'::regclass AND array_length(conkey, 1) = 2)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(pairing_computer_is_space_scoped);

    let computer_identity_hash_columns: Vec<String> = sqlx::query_scalar(
        "SELECT column_name FROM information_schema.columns \
         WHERE table_schema = 'public' AND table_name IN ('computers', 'computer_pairings') \
           AND column_name LIKE '%hash' AND column_name <> 'pairing_code_hash' \
         ORDER BY table_name, column_name",
    )
    .fetch_all(&pool)
    .await?;
    assert_eq!(
        computer_identity_hash_columns,
        vec!["token_hash".to_owned(), "token_hash".to_owned()]
    );

    let agent_computer_is_space_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'agents'::regclass \
         AND confrelid = 'computers'::regclass AND array_length(conkey, 1) = 2)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(agent_computer_is_space_scoped);

    let run_assignment_is_agent_scoped: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid = 'agent_runs'::regclass \
         AND confrelid = 'agents'::regclass AND array_length(conkey, 1) = 2)",
    )
    .fetch_one(&pool)
    .await?;
    assert!(run_assignment_is_agent_scoped);

    let one_active_run_is_enforced: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname = 'agent_runs_one_active_idx' \
         AND indexdef LIKE '%WHERE (status = ANY%')",
    )
    .fetch_one(&pool)
    .await?;
    assert!(one_active_run_is_enforced);

    let one_pending_thread_ambient_is_enforced: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_indexes \
         WHERE indexname = 'inbox_items_one_pending_thread_ambient_idx')",
    )
    .fetch_one(&pool)
    .await?;
    assert!(one_pending_thread_ambient_is_enforced);

    pool.close().await;
    Ok(())
}

async fn sumi_database_for_test(database_url: &str) -> Result<sqlx::PgPool> {
    let pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(2)
        .connect(database_url)
        .await?;
    sqlx::migrate!("./migrations/postgres").run(&pool).await?;
    Ok(pool)
}
