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
    pub(in crate::server) thread_id: ThreadId,
    pub(in crate::server) linked_by_member_id: MemberId,
    pub(in crate::server) linked_at: OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Task {
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
    pub(in crate::server) related_threads: Vec<RelatedThread>,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) updated_at: OffsetDateTime,
    pub(in crate::server) finished_at: Option<OffsetDateTime>,
}

impl Task {
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
