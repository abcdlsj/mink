use std::fmt;

use sha2::{Digest, Sha256};

use time::OffsetDateTime;

use crate::ids::{AgentId, InboxItemId, MessageId, NoticeId, TaskId, ThreadId};

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct RunInput {
    pub(in crate::computer) global_contract: String,
    pub(in crate::computer) agent: AgentInput,
    pub(in crate::computer) work: WorkInput,
    pub(in crate::computer) context: RunContextInput,
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct AgentInput {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) identity: String,
    pub(in crate::computer) role_revision: u64,
    pub(in crate::computer) role: String,
    pub(in crate::computer) memory_entry: String,
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct WorkInput {
    pub(in crate::computer) task: Option<TaskInput>,
    pub(in crate::computer) linked_thread_ids: Vec<ThreadId>,
    pub(in crate::computer) public_result_message_id: Option<MessageId>,
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct TaskInput {
    pub(in crate::computer) task_id: TaskId,
    pub(in crate::computer) title: String,
    pub(in crate::computer) status: String,
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct RunContextInput {
    pub(in crate::computer) focus_thread_id: ThreadId,
    pub(in crate::computer) message_snapshot_sequence: u64,
    pub(in crate::computer) focus_messages: Vec<String>,
    pub(in crate::computer) claimed_items: Vec<ClaimedItemInput>,
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct ClaimedItemInput {
    pub(in crate::computer) item_id: InboxItemId,
    pub(in crate::computer) task_id: Option<TaskId>,
    pub(in crate::computer) thread_id: ThreadId,
    pub(in crate::computer) content: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct AttentionNoticeInput {
    pub(in crate::computer) notice_id: NoticeId,
    pub(in crate::computer) source_kind: String,
    pub(in crate::computer) location: NoticeLocationInput,
    pub(in crate::computer) explicit_human_redirect: bool,
    pub(in crate::computer) arrived_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum NoticeLocationInput {
    Restricted,
    Visible {
        task_id: Option<TaskId>,
        thread_id: ThreadId,
    },
}

impl ClaimedItemInput {
    pub(in crate::computer) fn content_hash(&self) -> String {
        let mut digest = Sha256::new();
        digest.update(self.item_id.to_string().as_bytes());
        digest.update(format!("{:?}", self.task_id).as_bytes());
        digest.update(self.thread_id.to_string().as_bytes());
        if let Some(content) = &self.content {
            digest.update(content.as_bytes());
        }
        hex::encode(digest.finalize())
    }
}

impl fmt::Debug for ClaimedItemInput {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ClaimedItemInput")
            .field("item_id", &self.item_id)
            .field("task_id", &self.task_id)
            .field("thread_id", &self.thread_id)
            .field("has_content", &self.content.is_some())
            .finish()
    }
}

impl RunInput {
    pub(in crate::computer) fn content_hash(&self) -> String {
        let mut digest = Sha256::new();
        digest.update(self.global_contract.as_bytes());
        digest.update(self.agent.agent_id.to_string().as_bytes());
        digest.update(self.agent.identity.as_bytes());
        digest.update(self.agent.role_revision.to_le_bytes());
        digest.update(self.agent.role.as_bytes());
        digest.update(self.agent.memory_entry.as_bytes());
        if let Some(task) = &self.work.task {
            digest.update(task.task_id.to_string().as_bytes());
            digest.update(task.title.as_bytes());
            digest.update(task.status.as_bytes());
        }
        for thread_id in &self.work.linked_thread_ids {
            digest.update(thread_id.to_string().as_bytes());
        }
        if let Some(message_id) = self.work.public_result_message_id {
            digest.update(message_id.to_string().as_bytes());
        }
        digest.update(self.context.focus_thread_id.to_string().as_bytes());
        digest.update(self.context.message_snapshot_sequence.to_le_bytes());
        for message in &self.context.focus_messages {
            digest.update(message.as_bytes());
        }
        for item in &self.context.claimed_items {
            digest.update(item.content_hash().as_bytes());
        }
        hex::encode(digest.finalize())
    }
}

impl fmt::Debug for RunInput {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("RunInput")
            .field("agent_id", &self.agent.agent_id)
            .field("task_id", &self.work.task.as_ref().map(|task| task.task_id))
            .field("focus_thread_id", &self.context.focus_thread_id)
            .field("message_count", &self.context.focus_messages.len())
            .field("claimed_item_count", &self.context.claimed_items.len())
            .finish()
    }
}
