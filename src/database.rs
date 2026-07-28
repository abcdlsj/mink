use std::path::Path;

use anyhow::{Context, Result};
use sqlx::{
    PgPool, SqlitePool,
    postgres::PgPoolOptions,
    sqlite::{SqliteConnectOptions, SqlitePoolOptions},
};

static POSTGRES_MIGRATOR: sqlx::migrate::Migrator = sqlx::migrate!("./migrations/postgres");
static SQLITE_MIGRATOR: sqlx::migrate::Migrator = sqlx::migrate!("./migrations/sqlite");

pub async fn connect_postgres(database_url: &str) -> Result<PgPool> {
    let pool = PgPoolOptions::new()
        .max_connections(10)
        .connect(database_url)
        .await
        .context("failed to connect to PostgreSQL")?;
    POSTGRES_MIGRATOR
        .run(&pool)
        .await
        .context("failed to migrate PostgreSQL")?;
    Ok(pool)
}

pub async fn connect_sqlite(path: &Path) -> Result<SqlitePool> {
    let options = SqliteConnectOptions::new()
        .filename(path)
        .create_if_missing(true)
        .foreign_keys(true);
    let pool = SqlitePoolOptions::new()
        .max_connections(1)
        .connect_with(options)
        .await
        .context("failed to connect to daemon SQLite")?;
    SQLITE_MIGRATOR
        .run(&pool)
        .await
        .context("failed to migrate daemon SQLite")?;
    Ok(pool)
}

pub fn is_unique_constraint(error: &sqlx::Error, constraint: &str) -> bool {
    error
        .as_database_error()
        .and_then(|database| database.constraint())
        == Some(constraint)
}

#[cfg(test)]
mod tests {
    use tempfile::tempdir;

    use super::*;

    #[tokio::test]
    async fn sqlite_migrations_create_daemon_tables() {
        let directory = tempdir().unwrap();
        let database = connect_sqlite(&directory.path().join("daemon.db"))
            .await
            .unwrap();

        let table_count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN \
             ('daemon_metadata', 'server_commands', 'local_agent_runs', 'run_result_outbox', \
              'run_started_outbox')",
        )
        .fetch_one(&database)
        .await
        .unwrap();

        assert_eq!(table_count, 5);

        let ownership_columns: Vec<String> = sqlx::query_scalar(
            "SELECT name FROM pragma_table_info('local_agent_runs') \
             WHERE name IN ('fencing_token', 'ownership_lease_expires_at') ORDER BY name",
        )
        .fetch_all(&database)
        .await
        .unwrap();
        assert_eq!(
            ownership_columns,
            vec![
                "fencing_token".to_owned(),
                "ownership_lease_expires_at".to_owned(),
            ]
        );
    }
}
