#[cfg(test)]
use std::time::Duration;

use serde::Serialize;
use time::OffsetDateTime;

use crate::ids::{EventId, SpaceId};

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub(super) struct BrowserEvent<T> {
    pub(super) event_id: EventId,
    #[serde(rename = "type")]
    pub(super) event_type: String,
    pub(super) space_id: SpaceId,
    #[serde(with = "time::serde::rfc3339")]
    pub(super) occurred_at: OffsetDateTime,
    pub(super) data: T,
}

#[cfg(test)]
pub(super) struct EventWindow<T> {
    retention: Duration,
    events: VecDeque<BrowserEvent<T>>,
}

#[cfg(test)]
impl<T: Clone> EventWindow<T> {
    pub(super) fn new(retention: Duration) -> Self {
        Self {
            retention,
            events: VecDeque::new(),
        }
    }

    pub(super) fn publish(&mut self, event: BrowserEvent<T>) {
        let cutoff = event.occurred_at - self.retention;
        while self
            .events
            .front()
            .is_some_and(|stored| stored.occurred_at < cutoff)
        {
            self.events.pop_front();
        }
        self.events.push_back(event);
    }

    pub(super) fn resume_after(
        &self,
        last_event_id: Option<EventId>,
    ) -> Option<Vec<BrowserEvent<T>>> {
        let Some(last_event_id) = last_event_id else {
            return Some(self.events.iter().cloned().collect());
        };
        let position = self
            .events
            .iter()
            .position(|event| event.event_id == last_event_id)?;
        Some(self.events.iter().skip(position + 1).cloned().collect())
    }
}

impl<T: Serialize> BrowserEvent<T> {
    pub(super) fn into_sse(self) -> Result<axum::response::sse::Event, axum::Error> {
        let id = self.event_id.to_string();
        let event_type = self.event_type.clone();
        axum::response::sse::Event::default()
            .id(id)
            .event(event_type)
            .json_data(self)
    }
}

use super::http::{ApiError, RuntimeState, application_error, current_member};
use crate::ids::MemberId;
use axum::{
    extract::{Path, State},
    http::HeaderMap,
    response::{
        Sse,
        sse::{Event as SseEvent, KeepAlive},
    },
};
use axum_extra::extract::cookie::CookieJar;
use futures_util::stream;
use std::{collections::VecDeque, convert::Infallible};
use uuid::Uuid;

pub(super) async fn space_events(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
) -> Result<Sse<impl futures_util::Stream<Item = Result<SseEvent, Infallible>>>, ApiError> {
    let viewer = current_member(&state, &jar, space_id).await?;
    let space_id = SpaceId::from_uuid(space_id);
    let last_event_id = headers
        .get("last-event-id")
        .map(|value| {
            value
                .to_str()
                .ok()
                .and_then(|value| value.parse().ok())
                .ok_or_else(|| ApiError::invalid("Last-Event-ID is invalid"))
        })
        .transpose()?;
    let viewer = MemberId::from_uuid(viewer);
    let initial = state
        .storage
        .browser_events(space_id, viewer, last_event_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::context_changed)?;
    let events = stream::unfold(
        (
            state.storage,
            space_id,
            last_event_id,
            VecDeque::from(initial),
        ),
        move |(storage, space_id, mut cursor, mut buffered)| async move {
            loop {
                if let Some(event) = buffered.pop_front() {
                    cursor = Some(event.event_id);
                    let event = match event.into_sse() {
                        Ok(event) => event,
                        Err(_) => return None,
                    };
                    return Some((Ok(event), (storage, space_id, cursor, buffered)));
                }
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                buffered = match storage.browser_events(space_id, viewer, cursor).await {
                    Ok(Some(events)) => VecDeque::from(events),
                    Ok(None) | Err(_) => return None,
                };
            }
        },
    );
    Ok(Sse::new(events).keep_alive(KeepAlive::default()))
}

#[cfg(test)]
mod tests {
    use uuid::Uuid;

    use super::*;

    #[test]
    fn expired_sse_cursor_requires_projection_reload() {
        let mut window = EventWindow::new(Duration::from_secs(60));
        let first = EventId::from_uuid(Uuid::now_v7());
        let second = EventId::from_uuid(Uuid::now_v7());
        let space_id = SpaceId::from_uuid(Uuid::now_v7());
        window.publish(BrowserEvent {
            event_id: first,
            event_type: "task.updated".into(),
            space_id,
            occurred_at: OffsetDateTime::UNIX_EPOCH,
            data: (),
        });
        window.publish(BrowserEvent {
            event_id: second,
            event_type: "task.finished".into(),
            space_id,
            occurred_at: OffsetDateTime::UNIX_EPOCH + Duration::from_secs(61),
            data: (),
        });

        assert!(window.resume_after(Some(first)).is_none());
        assert!(window.resume_after(Some(second)).unwrap().is_empty());
    }
}
