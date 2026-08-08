use std::{collections::BTreeMap, fmt};

use serde::{Deserialize, Serialize};

use crate::ids::{
    AgentId, AttachmentId, ChannelId, ComputerId, IdempotencyKey, InboxItemId, MemberId, MessageId,
    RunId, SpaceId, TaskId, ThreadId,
};

pub(crate) const SCHEMA_VERSION: u16 = 1;

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Request {
    pub(crate) schema_version: u16,
    pub(crate) driver_token: String,
    pub(crate) idempotency_key: Option<IdempotencyKey>,
    pub(crate) action: Action,
}

impl fmt::Debug for Request {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("Request")
            .field("schema_version", &self.schema_version)
            .field("driver_token", &"[REDACTED]")
            .field("idempotency_key", &self.idempotency_key)
            .field("action", &self.action.name())
            .finish()
    }
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(
    tag = "type",
    content = "input",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub(crate) enum Action {
    Discover {
        operation: String,
    },
    ContextCurrent,
    SpaceMembers,
    MessageRead(Page),
    ThreadRead {
        thread_id: ThreadId,
        page: Page,
    },
    ChannelMembers {
        channel_id: ChannelId,
    },
    ChannelRead {
        channel_id: ChannelId,
        around_message_id: Option<MessageId>,
        limit: u16,
    },
    MessageSend(MessageSend),
    TaskCreate {
        title: Option<String>,
        assignee: Option<MemberId>,
    },
    TaskOpen,
    TaskStart {
        task: TaskReference,
    },
    TaskLinkThread {
        thread_id: ThreadId,
    },
    TaskUnlinkThread {
        thread_id: ThreadId,
    },
    TaskSubmitReview {
        body: String,
        post_to: PostTarget,
    },
    TaskDone {
        result: String,
        post_to: PostTarget,
    },
    TaskClose {
        reason: CloseReason,
        note: Option<String>,
    },
    RunYield {
        note: Option<String>,
    },
    InboxCurrent,
    InboxAck {
        item_id: InboxItemId,
        reason: Option<String>,
    },
    InboxDefer {
        item_id: InboxItemId,
        until: time::OffsetDateTime,
    },
    AttachmentUpload {
        path: String,
    },
    AttachmentDownload {
        attachment_id: AttachmentId,
        output: String,
    },
    MemoryRead {
        path: String,
    },
    MemoryWrite {
        path: String,
        content: String,
    },
    ChannelCreate {
        slug: String,
        topic: Option<String>,
        private: bool,
    },
    ChannelLeave {
        channel_id: ChannelId,
    },
    ChannelInvite {
        channel_id: ChannelId,
        member_id: MemberId,
    },
    ChannelRemove {
        channel_id: ChannelId,
        member_id: MemberId,
    },
    AgentCreate {
        name: String,
        role: String,
        driver: DriverKind,
        computer_id: ComputerId,
    },
}

impl Action {
    pub(crate) fn name(&self) -> &'static str {
        match self {
            Self::Discover { .. } => "discover",
            Self::ContextCurrent => "context.current",
            Self::SpaceMembers => "space.members",
            Self::MessageRead(_) => "message.read",
            Self::ThreadRead { .. } => "thread.read",
            Self::ChannelMembers { .. } => "channel.members",
            Self::ChannelRead { .. } => "channel.read",
            Self::MessageSend(_) => "message.send",
            Self::TaskCreate { .. } => "task.create",
            Self::TaskOpen => "task.open",
            Self::TaskStart { .. } => "task.start",
            Self::TaskLinkThread { .. } => "task.link_thread",
            Self::TaskUnlinkThread { .. } => "task.unlink_thread",
            Self::TaskSubmitReview { .. } => "task.submit_review",
            Self::TaskDone { .. } => "task.done",
            Self::TaskClose { .. } => "task.close",
            Self::RunYield { .. } => "run.yield",
            Self::InboxCurrent => "inbox.current",
            Self::InboxAck { .. } => "inbox.ack",
            Self::InboxDefer { .. } => "inbox.defer",
            Self::AttachmentUpload { .. } => "attachment.upload",
            Self::AttachmentDownload { .. } => "attachment.download",
            Self::MemoryRead { .. } => "memory.read",
            Self::MemoryWrite { .. } => "memory.write",
            Self::ChannelCreate { .. } => "channel.create",
            Self::ChannelLeave { .. } => "channel.leave",
            Self::ChannelInvite { .. } => "channel.invite",
            Self::ChannelRemove { .. } => "channel.remove",
            Self::AgentCreate { .. } => "agent.create",
        }
    }

    pub(crate) fn requires_task(&self) -> bool {
        matches!(
            self,
            Self::TaskLinkThread { .. }
                | Self::TaskUnlinkThread { .. }
                | Self::TaskSubmitReview { .. }
                | Self::TaskDone { .. }
                | Self::TaskClose { .. }
        )
    }
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(untagged)]
pub(crate) enum TaskReference {
    Seq(u64),
    Id(TaskId),
}

impl fmt::Debug for Action {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.name())
    }
}

#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Page {
    pub(crate) before: Option<u64>,
    pub(crate) after: Option<u64>,
    pub(crate) limit: u16,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MessageSend {
    pub(crate) target: MessageTarget,
    pub(crate) body: String,
    pub(crate) attachment_ids: Vec<AttachmentId>,
    pub(crate) handle_item_id: Option<InboxItemId>,
    pub(crate) snapshot_sequence: Option<u64>,
}

impl fmt::Debug for MessageSend {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("MessageSend")
            .field("target", &self.target)
            .field("body", &"[REDACTED]")
            .field("attachment_ids", &self.attachment_ids)
            .field("handle_item_id", &self.handle_item_id)
            .field("snapshot_sequence", &self.snapshot_sequence)
            .finish()
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(
    tag = "kind",
    content = "id",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub(crate) enum MessageTarget {
    Focus,
    Thread(ThreadId),
    Channel(ChannelId),
    Member(MemberId),
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum PostTarget {
    Focus,
    Source,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum CloseReason {
    Invalid,
    Duplicate,
    NotNeeded,
    Obsolete,
    Other,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum DriverKind {
    Codex,
    Builtin,
}

/// Identifies which Run a capability call belongs to. Carries no credential: the Computer's own token
/// authenticates the caller, and the Run being `working` on that Computer authorizes the call.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunContext {
    pub(crate) agent_id: AgentId,
    pub(crate) space_id: SpaceId,
    pub(crate) task_id: Option<TaskId>,
    pub(crate) focus_thread_id: ThreadId,
    pub(crate) run_id: RunId,
    pub(crate) message_snapshot_sequence: u64,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ErrorCode {
    InvalidArgument,
    Unauthenticated,
    PermissionDenied,
    NotFound,
    Conflict,
    ContextChanged,
    RateLimited,
    Unavailable,
    Internal,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Error {
    pub(crate) code: ErrorCode,
    pub(crate) message: String,
    pub(crate) retryable: bool,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub(crate) details: BTreeMap<String, serde_json::Value>,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Response<T> {
    pub(crate) schema_version: u16,
    pub(crate) ok: bool,
    pub(crate) data: Option<T>,
    pub(crate) error: Option<Error>,
}

impl<T> Response<T> {
    pub(crate) fn success(data: T) -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            ok: true,
            data: Some(data),
            error: None,
        }
    }

    pub(crate) fn failure(error: Error) -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            ok: false,
            data: None,
            error: Some(error),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{
        Action, ErrorCode, MessageSend, MessageTarget, Request, SCHEMA_VERSION, TaskReference,
    };

    #[test]
    fn error_code_has_stable_wire_value() {
        assert_eq!(
            serde_json::to_string(&ErrorCode::PermissionDenied).unwrap(),
            "\"permission_denied\""
        );
    }

    #[test]
    fn request_debug_excludes_token_and_content() {
        let request = Request {
            schema_version: SCHEMA_VERSION,
            driver_token: "secret-token".to_owned(),
            idempotency_key: None,
            action: Action::MessageSend(MessageSend {
                target: MessageTarget::Focus,
                body: "private body".to_owned(),
                attachment_ids: Vec::new(),
                handle_item_id: None,
                snapshot_sequence: None,
            }),
        };
        let debug = format!("{request:?}");
        assert!(!debug.contains("secret-token"));
        assert!(!debug.contains("private body"));
        assert!(debug.contains("message.send"));
    }

    #[test]
    fn task_open_and_start_round_trip_through_the_wire_shape() {
        let open: Action =
            serde_json::from_value(serde_json::json!({"type": "task_open"})).unwrap();
        assert_eq!(open, Action::TaskOpen);
        assert_eq!(
            serde_json::to_value(&open).unwrap(),
            serde_json::json!({"type": "task_open"})
        );
        let start: Action =
            serde_json::from_value(serde_json::json!({"type": "task_start", "input": {"task": 7}}))
                .unwrap();
        assert_eq!(
            start,
            Action::TaskStart {
                task: TaskReference::Seq(7)
            }
        );
    }
}
