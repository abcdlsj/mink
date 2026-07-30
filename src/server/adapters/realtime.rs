#[cfg(test)]
use std::{collections::VecDeque, time::Duration};

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
