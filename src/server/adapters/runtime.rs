use super::{
    http,
    object_storage::AttachmentObjectStore,
    postgres::{PostgresAdapter, PostgresQueries},
    query::QueryRegistry,
};
use crate::config::ServerConfig;
use crate::server::domain::access::SessionLifetime;
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
        #[cfg(test)]
        pool: pool.clone(),
        storage,
        read: PostgresQueries::new(pool.clone()),
        objects: std::sync::Arc::new(AttachmentObjectStore::new(std::sync::Arc::new(
            object_store,
        ))),
        session_lifetime: SessionLifetime::from_hours(config.session_ttl_hours)
            .context("Server session TTL must be a positive number of hours")?,
        attachment_max_bytes: config.attachment_max_bytes,
        queries: QueryRegistry::default(),
    };
    let api = http::api_router(state.clone(), 100 * 1024 * 1024);
    let dispatcher = tokio::spawn(dispatch_available_work_forever(state.clone()));
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
    dispatcher.abort();
    served
}

/// How often the Server looks for Agents with work and no live Run.
///
/// Short because it bounds how long an Agent waits before starting, and nothing else: no Run outcome
/// depends on this timer, so a slow or skipped tick delays work and cannot fail it. A tick is needed
/// at all because availability is a future fact, set by ambient debounce and by Agent-chosen defer
/// times.
const DISPATCH_INTERVAL: std::time::Duration = std::time::Duration::from_secs(1);
/// Agents dispatched per tick. Caps one pass, not total throughput: the next tick takes the rest.
const DISPATCH_BATCH: u32 = 64;

async fn dispatch_available_work_forever(state: http::RuntimeState) {
    let mut ticker = tokio::time::interval(DISPATCH_INTERVAL);
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    loop {
        ticker.tick().await;
        match http::dispatch_available_work(&state, OffsetDateTime::now_utc(), DISPATCH_BATCH).await
        {
            Ok(outcome) if outcome.failed > 0 => tracing::warn!(
                dispatched = outcome.dispatched,
                failed = outcome.failed,
                "some Runs could not be dispatched; their Items stay pending for the next pass"
            ),
            Ok(_) => {}
            Err(error) => {
                tracing::error!(?error, "dispatch pass failed; retrying on the next tick")
            }
        }
    }
}
async fn shutdown_signal() {
    let _ = tokio::signal::ctrl_c().await;
}
