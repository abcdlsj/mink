use std::{convert::Infallible, time::Duration};

use axum::{
    extract::{Path, State},
    http::HeaderMap,
    response::sse::{Event, KeepAlive, Sse},
};
use axum_extra::extract::CookieJar;
use futures_util::stream::{self, Stream};
use serde::Serialize;
use sqlx::FromRow;
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, member};

#[derive(FromRow)]
struct OutboxRow {
    id: Uuid,
    topic: String,
    payload_json: serde_json::Value,
    created_at: OffsetDateTime,
}

#[derive(Serialize)]
struct EventEnvelope {
    event_id: Uuid,
    #[serde(rename = "type")]
    kind: String,
    space_id: Uuid,
    occurred_at: OffsetDateTime,
    data: serde_json::Value,
}

struct StreamState {
    database: sqlx::PgPool,
    space_id: Uuid,
    cursor: Option<Uuid>,
    pending: Vec<OutboxRow>,
}

pub async fn events(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
) -> Result<Sse<impl Stream<Item = Result<Event, Infallible>>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    member::require_actor(&state.database, user.id, space_id).await?;
    let cursor = match headers.get("last-event-id") {
        Some(value) => Some(
            value
                .to_str()
                .ok()
                .and_then(|value| Uuid::parse_str(value).ok())
                .ok_or_else(|| {
                    ApiError::validation("invalid_event_cursor", "Last-Event-ID must be a UUID")
                })?,
        ),
        None => sqlx::query_scalar(
            "SELECT max(id) FROM outbox_events WHERE payload_json->>'space_id' = $1::text",
        )
        .bind(space_id)
        .fetch_one(&state.database)
        .await
        .map_err(ApiError::database)?,
    };
    let stream = stream::unfold(
        StreamState {
            database: state.database.clone(),
            space_id,
            cursor,
            pending: Vec::new(),
        },
        |mut state| async move {
            loop {
                if let Some(row) = state.pending.pop() {
                    state.cursor = Some(row.id);
                    let envelope = EventEnvelope {
                        event_id: row.id,
                        kind: row.topic.clone(),
                        space_id: state.space_id,
                        occurred_at: row.created_at,
                        data: row.payload_json,
                    };
                    let data = match serde_json::to_string(&envelope) {
                        Ok(data) => data,
                        Err(error) => {
                            tracing::error!(error = %error, "failed to serialize SSE event");
                            continue;
                        }
                    };
                    let event = Event::default()
                        .id(row.id.to_string())
                        .event(row.topic)
                        .data(data);
                    return Some((Ok(event), state));
                }

                match fetch_batch(&state.database, state.space_id, state.cursor).await {
                    Ok(mut rows) if !rows.is_empty() => {
                        let ids = rows.iter().map(|row| row.id).collect::<Vec<_>>();
                        if let Err(error) = sqlx::query(
                            "UPDATE outbox_events SET published_at = COALESCE(published_at, now()), \
                             attempts = attempts + 1 WHERE id = ANY($1)",
                        )
                        .bind(&ids)
                        .execute(&state.database)
                        .await
                        {
                            tracing::error!(error = %error, "failed to mark outbox events published");
                        }
                        rows.reverse();
                        state.pending = rows;
                    }
                    Ok(_) => tokio::time::sleep(Duration::from_millis(400)).await,
                    Err(error) => {
                        tracing::error!(error = %error, "failed to poll Browser outbox");
                        tokio::time::sleep(Duration::from_secs(1)).await;
                    }
                }
            }
        },
    );
    Ok(Sse::new(stream).keep_alive(
        KeepAlive::new()
            .interval(Duration::from_secs(15))
            .text("keep-alive"),
    ))
}

async fn fetch_batch(
    database: &sqlx::PgPool,
    space_id: Uuid,
    cursor: Option<Uuid>,
) -> Result<Vec<OutboxRow>, sqlx::Error> {
    sqlx::query_as(
        "SELECT id, topic, payload_json, created_at FROM outbox_events \
         WHERE payload_json->>'space_id' = $1::text AND ($2::uuid IS NULL OR id > $2) \
         ORDER BY id LIMIT 100",
    )
    .bind(space_id)
    .bind(cursor)
    .fetch_all(database)
    .await
}
