use super::{
    http, object_storage::AttachmentObjectStore, postgres::PostgresAdapter, query::QueryRegistry,
};
use crate::config::ServerConfig;
use crate::server::domain::{access::SessionLifetime, attention::AttentionPolicy};
use anyhow::Context;
use sqlx::postgres::PgPoolOptions;
use time::OffsetDateTime;
use tower_http::{
    services::{ServeDir, ServeFile},
    trace::TraceLayer,
};

pub(in crate::server) async fn run(config: ServerConfig) -> anyhow::Result<()> {
    let pool = PgPoolOptions::new()
        .max_connections(10)
        .connect(&config.database_url)
        .await
        .context("failed to connect to PostgreSQL")?;
    let storage = PostgresAdapter::new(pool.clone());
    storage
        .initialize_schema()
        .await
        .context("failed to initialize PostgreSQL schema")?;
    tokio::fs::create_dir_all(&config.attachment_dir)
        .await
        .context("failed to create Attachment directory")?;
    let object_store =
        object_store::local::LocalFileSystem::new_with_prefix(&config.attachment_dir)
            .context("failed to open Attachment directory")?;
    let state = http::RuntimeState {
        pool,
        storage,
        objects: std::sync::Arc::new(AttachmentObjectStore::new(std::sync::Arc::new(
            object_store,
        ))),
        session_lifetime: SessionLifetime::from_hours(config.session_ttl_hours)
            .context("Server session TTL must be a positive number of hours")?,
        attachment_max_bytes: config.attachment_max_bytes,
        queries: QueryRegistry::default(),
    };
    let api = http::api_router(state.clone(), 100 * 1024 * 1024);
    let reclaim = tokio::spawn(reclaim_expired_leases_forever(state.storage.clone()));
    let app = axum::Router::new()
        .nest("/api/v1", api)
        .fallback_service(
            ServeDir::new(&config.web_dist)
                .append_index_html_on_directories(true)
                .not_found_service(ServeFile::new(config.web_dist.join("index.html"))),
        )
        .layer(TraceLayer::new_for_http());
    let listener = tokio::net::TcpListener::bind(config.bind)
        .await
        .with_context(|| format!("failed to bind Server at {}", config.bind))?;
    let served = axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await
        .context("Server stopped unexpectedly");
    reclaim.abort();
    served
}

const LEASE_RECLAIM_INTERVAL: std::time::Duration = std::time::Duration::from_secs(30);
const LEASE_RECLAIM_BATCH: u32 = 50;
async fn reclaim_expired_leases_forever(mut storage: PostgresAdapter) {
    let mut ticker = tokio::time::interval(LEASE_RECLAIM_INTERVAL);
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    loop {
        ticker.tick().await;
        match crate::server::application::execution::ReclaimExpiredLeases::execute(
            &mut storage,
            crate::server::application::execution::ReclaimExpiredLeasesInput {
                now: OffsetDateTime::now_utc(),
                limit: LEASE_RECLAIM_BATCH,
                max_retry_count: AttentionPolicy::MAX_RETRY_COUNT,
            },
        )
        .await
        {
            Ok(reclaimed) if reclaimed.runs_failed > 0 => tracing::warn!(
                runs_failed = reclaimed.runs_failed,
                items_released = reclaimed.items_released,
                items_dead = reclaimed.items_dead,
                "reclaimed Runs whose ownership lease expired"
            ),
            Ok(_) => {}
            Err(error) => tracing::error!(
                ?error,
                "lease reclaim sweep failed; retrying on the next tick"
            ),
        }
    }
}
async fn shutdown_signal() {
    let _ = tokio::signal::ctrl_c().await;
}
