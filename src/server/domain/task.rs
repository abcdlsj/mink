use time::OffsetDateTime;

use crate::ids::{MemberId, MessageId, SpaceId, TaskId, ThreadId};

use super::{
    DomainError,
    conversation::{Message, Thread},
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum TaskStatus {
    Todo,
    InProgress,
    InReview,
    Done,
    Closed,
}

impl TaskStatus {
    pub(in crate::server) fn is_finished(self) -> bool {
        matches!(self, Self::Done | Self::Closed)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum CloseReason {
    Invalid,
    Duplicate,
    NotNeeded,
    Obsolete,
    Other,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct RelatedThread {
    thread_id: ThreadId,
    linked_by_member_id: MemberId,
    linked_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct RelatedThreadSnapshot {
    pub(in crate::server) thread_id: ThreadId,
    pub(in crate::server) linked_by_member_id: MemberId,
    pub(in crate::server) linked_at: OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Task {
    id: TaskId,
    space_id: SpaceId,
    title: String,
    status: TaskStatus,
    source_thread_id: ThreadId,
    creator_member_id: MemberId,
    assignee_agent_member_id: Option<MemberId>,
    result_message_id: Option<MessageId>,
    close_reason: Option<CloseReason>,
    close_reason_note: Option<String>,
    related_threads: Vec<RelatedThread>,
    created_at: OffsetDateTime,
    updated_at: OffsetDateTime,
    finished_at: Option<OffsetDateTime>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct TaskView<'a> {
    pub(in crate::server) id: TaskId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) title: &'a str,
    pub(in crate::server) status: TaskStatus,
    pub(in crate::server) source_thread_id: ThreadId,
    pub(in crate::server) creator_member_id: MemberId,
    pub(in crate::server) assignee_agent_member_id: Option<MemberId>,
    pub(in crate::server) result_message_id: Option<MessageId>,
    pub(in crate::server) close_reason: Option<CloseReason>,
    pub(in crate::server) close_reason_note: Option<&'a str>,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) updated_at: OffsetDateTime,
    pub(in crate::server) finished_at: Option<OffsetDateTime>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct TaskSnapshot {
    pub(in crate::server) id: TaskId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) title: String,
    pub(in crate::server) status: TaskStatus,
    pub(in crate::server) source_thread_id: ThreadId,
    pub(in crate::server) creator_member_id: MemberId,
    pub(in crate::server) assignee_agent_member_id: Option<MemberId>,
    pub(in crate::server) result_message_id: Option<MessageId>,
    pub(in crate::server) close_reason: Option<CloseReason>,
    pub(in crate::server) close_reason_note: Option<String>,
    pub(in crate::server) related_threads: Vec<RelatedThreadSnapshot>,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) updated_at: OffsetDateTime,
    pub(in crate::server) finished_at: Option<OffsetDateTime>,
}

impl Task {
    pub(in crate::server) fn view(&self) -> TaskView<'_> {
        TaskView {
            id: self.id,
            space_id: self.space_id,
            title: &self.title,
            status: self.status,
            source_thread_id: self.source_thread_id,
            creator_member_id: self.creator_member_id,
            assignee_agent_member_id: self.assignee_agent_member_id,
            result_message_id: self.result_message_id,
            close_reason: self.close_reason,
            close_reason_note: self.close_reason_note.as_deref(),
            created_at: self.created_at,
            updated_at: self.updated_at,
            finished_at: self.finished_at,
        }
    }

    pub(in crate::server) fn related_threads(
        &self,
    ) -> impl Iterator<Item = RelatedThreadSnapshot> + '_ {
        self.related_threads
            .iter()
            .map(|link| RelatedThreadSnapshot {
                thread_id: link.thread_id,
                linked_by_member_id: link.linked_by_member_id,
                linked_at: link.linked_at,
            })
    }

    pub(in crate::server) fn snapshot(&self) -> TaskSnapshot {
        let view = self.view();
        TaskSnapshot {
            id: view.id,
            space_id: view.space_id,
            title: view.title.to_owned(),
            status: view.status,
            source_thread_id: view.source_thread_id,
            creator_member_id: view.creator_member_id,
            assignee_agent_member_id: view.assignee_agent_member_id,
            result_message_id: view.result_message_id,
            close_reason: view.close_reason,
            close_reason_note: view.close_reason_note.map(str::to_owned),
            related_threads: self.related_threads().collect(),
            created_at: view.created_at,
            updated_at: view.updated_at,
            finished_at: view.finished_at,
        }
    }

    pub(in crate::server) fn rehydrate(snapshot: TaskSnapshot) -> Result<Self, DomainError> {
        let state_is_valid = match snapshot.status {
            TaskStatus::Done => {
                snapshot.result_message_id.is_some()
                    && snapshot.close_reason.is_none()
                    && snapshot.close_reason_note.is_none()
                    && snapshot.finished_at.is_some()
            }
            TaskStatus::Closed => {
                snapshot.result_message_id.is_none()
                    && snapshot.close_reason.is_some()
                    && snapshot.finished_at.is_some()
            }
            TaskStatus::Todo | TaskStatus::InProgress | TaskStatus::InReview => {
                snapshot.result_message_id.is_none()
                    && snapshot.close_reason.is_none()
                    && snapshot.close_reason_note.is_none()
                    && snapshot.finished_at.is_none()
                    && (!matches!(
                        snapshot.status,
                        TaskStatus::InProgress | TaskStatus::InReview
                    ) || snapshot.assignee_agent_member_id.is_some())
            }
        };
        let mut thread_ids = std::collections::BTreeSet::new();
        if !state_is_valid
            || snapshot.related_threads.iter().any(|link| {
                link.thread_id == snapshot.source_thread_id || !thread_ids.insert(link.thread_id)
            })
        {
            return Err(DomainError::InvalidPersistedState);
        }
        Ok(Self {
            id: snapshot.id,
            space_id: snapshot.space_id,
            title: snapshot.title,
            status: snapshot.status,
            source_thread_id: snapshot.source_thread_id,
            creator_member_id: snapshot.creator_member_id,
            assignee_agent_member_id: snapshot.assignee_agent_member_id,
            result_message_id: snapshot.result_message_id,
            close_reason: snapshot.close_reason,
            close_reason_note: snapshot.close_reason_note,
            related_threads: snapshot
                .related_threads
                .into_iter()
                .map(|link| RelatedThread {
                    thread_id: link.thread_id,
                    linked_by_member_id: link.linked_by_member_id,
                    linked_at: link.linked_at,
                })
                .collect(),
            created_at: snapshot.created_at,
            updated_at: snapshot.updated_at,
            finished_at: snapshot.finished_at,
        })
    }

    #[allow(clippy::too_many_arguments)]
    pub(in crate::server) fn create(
        id: TaskId,
        title: String,
        creator_member_id: MemberId,
        assignee_agent_member_id: Option<MemberId>,
        source: &Thread,
        root: &Message,
        created_by_running_agent: bool,
        now: OffsetDateTime,
    ) -> Result<Self, DomainError> {
        source.validate_source(root)?;
        if created_by_running_agent && assignee_agent_member_id.is_none() {
            return Err(DomainError::AssigneeRequired);
        }
        Ok(Self {
            id,
            space_id: source.space_id,
            title,
            status: if created_by_running_agent {
                TaskStatus::InProgress
            } else {
                TaskStatus::Todo
            },
            source_thread_id: source.id,
            creator_member_id,
            assignee_agent_member_id,
            result_message_id: None,
            close_reason: None,
            close_reason_note: None,
            related_threads: Vec::new(),
            created_at: now,
            updated_at: now,
            finished_at: None,
        })
    }

    pub(in crate::server) fn linked_to(&self, thread_id: ThreadId) -> bool {
        self.source_thread_id == thread_id
            || self
                .related_threads
                .iter()
                .any(|link| link.thread_id == thread_id)
    }

    pub(in crate::server) fn rename(&mut self, title: String, now: OffsetDateTime) {
        self.title = title;
        self.updated_at = now;
    }

    pub(in crate::server) fn add_related_thread(
        &mut self,
        source: &Thread,
        target: &Thread,
        actor: MemberId,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if target.id == self.source_thread_id {
            return Err(DomainError::SourceThreadImmutable);
        }
        if !source.has_same_audience(target) {
            return Err(DomainError::IncompatibleAudience);
        }
        if !self.linked_to(target.id) {
            self.related_threads.push(RelatedThread {
                thread_id: target.id,
                linked_by_member_id: actor,
                linked_at: now,
            });
            self.updated_at = now;
        }
        Ok(())
    }

    pub(in crate::server) fn remove_related_thread(
        &mut self,
        thread_id: ThreadId,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if thread_id == self.source_thread_id {
            return Err(DomainError::SourceThreadImmutable);
        }
        self.related_threads
            .retain(|link| link.thread_id != thread_id);
        self.updated_at = now;
        Ok(())
    }

    pub(in crate::server) fn start(
        &mut self,
        assignee: MemberId,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != TaskStatus::Todo {
            return Err(DomainError::InvalidTransition);
        }
        self.assignee_agent_member_id = Some(assignee);
        self.status = TaskStatus::InProgress;
        self.updated_at = now;
        Ok(())
    }

    pub(in crate::server) fn request_review(
        &mut self,
        actor: MemberId,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != TaskStatus::InProgress || self.assignee_agent_member_id != Some(actor) {
            return Err(DomainError::InvalidTransition);
        }
        self.status = TaskStatus::InReview;
        self.updated_at = now;
        Ok(())
    }

    pub(in crate::server) fn return_from_review(
        &mut self,
        actor: MemberId,
        can_read: bool,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        self.validate_reviewer(actor, can_read)?;
        self.status = TaskStatus::InProgress;
        self.updated_at = now;
        Ok(())
    }

    pub(in crate::server) fn finish(
        &mut self,
        actor: MemberId,
        can_read: bool,
        result_message_id: MessageId,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        match self.status {
            TaskStatus::InProgress if self.assignee_agent_member_id == Some(actor) => {}
            TaskStatus::InReview => self.validate_reviewer(actor, can_read)?,
            _ => return Err(DomainError::InvalidTransition),
        }
        self.status = TaskStatus::Done;
        self.result_message_id = Some(result_message_id);
        self.updated_at = now;
        self.finished_at = Some(now);
        Ok(())
    }

    pub(in crate::server) fn close(
        &mut self,
        reason: CloseReason,
        note: Option<String>,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status.is_finished() {
            return Err(DomainError::InvalidTransition);
        }
        self.status = TaskStatus::Closed;
        self.close_reason = Some(reason);
        self.close_reason_note = note;
        self.updated_at = now;
        self.finished_at = Some(now);
        Ok(())
    }

    fn validate_reviewer(&self, actor: MemberId, can_read: bool) -> Result<(), DomainError> {
        if self.status != TaskStatus::InReview
            || self.assignee_agent_member_id == Some(actor)
            || !can_read
        {
            return Err(DomainError::InvalidReviewer);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn snapshot() -> TaskSnapshot {
        TaskSnapshot {
            id: TaskId::from_uuid(uuid::Uuid::from_u128(1)),
            space_id: SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            title: "任务".into(),
            status: TaskStatus::InProgress,
            source_thread_id: ThreadId::from_uuid(uuid::Uuid::from_u128(3)),
            creator_member_id: MemberId::from_uuid(uuid::Uuid::from_u128(4)),
            assignee_agent_member_id: Some(MemberId::from_uuid(uuid::Uuid::from_u128(5))),
            result_message_id: None,
            close_reason: None,
            close_reason_note: None,
            related_threads: Vec::new(),
            created_at: OffsetDateTime::UNIX_EPOCH,
            updated_at: OffsetDateTime::UNIX_EPOCH,
            finished_at: None,
        }
    }

    #[test]
    fn rehydrate_accepts_a_consistent_snapshot_and_rejects_a_live_result() {
        let snapshot = snapshot();
        let restored = Task::rehydrate(snapshot.clone()).expect("snapshot is valid");
        assert_eq!(restored.snapshot(), snapshot);

        let mut invalid = snapshot;
        invalid.result_message_id = Some(MessageId::from_uuid(uuid::Uuid::from_u128(6)));
        assert_eq!(
            Task::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );
    }
}
