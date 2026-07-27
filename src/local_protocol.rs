use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Deserialize, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum LocalRequest {
    Whoami {
        run_token: String,
    },
    AgentAction {
        run_token: String,
        action: AgentAction,
    },
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(tag = "action", rename_all = "snake_case")]
pub enum AgentAction {
    MemberList {
        query: Option<String>,
    },
    ChannelList,
    InboxCurrent,
    InboxShow {
        inbox_item_id: Uuid,
    },
    ChannelRead {
        address: String,
        before: Option<i64>,
        #[serde(default)]
        after: Option<i64>,
        #[serde(default)]
        around: Option<Uuid>,
        limit: i64,
    },
    ChannelCreate {
        slug: String,
        name: String,
        private: bool,
        idempotency_key: Uuid,
    },
    ChannelMemberAdd {
        address: String,
        member_id: Uuid,
        idempotency_key: Uuid,
    },
    ChannelMemberRemove {
        address: String,
        member_id: Uuid,
        idempotency_key: Uuid,
    },
    ChannelArchive {
        address: String,
        idempotency_key: Uuid,
    },
    ThreadRead {
        address: String,
        after: Option<i64>,
        limit: i64,
        include_channel: i64,
    },
    MessageSend {
        address: String,
        body_markdown: String,
        based_on: Option<i64>,
        handle_inbox_item_id: Option<Uuid>,
        #[serde(default)]
        attachment_ids: Vec<Uuid>,
        idempotency_key: Uuid,
    },
    InboxAck {
        inbox_item_ids: Vec<Uuid>,
        reason: String,
        idempotency_key: Uuid,
    },
    InboxDefer {
        inbox_item_ids: Vec<Uuid>,
        until: time::OffsetDateTime,
        idempotency_key: Uuid,
    },
    AttachmentUpload {
        path: String,
        media_type: String,
        idempotency_key: Uuid,
    },
    AttachmentDownload {
        attachment_id: Uuid,
        output_path: String,
    },
    AttachmentInfo {
        attachment_id: Uuid,
    },
    AgentCreate {
        name: String,
        role_text: String,
        computer_id: Uuid,
        driver_kind: String,
        idempotency_key: Uuid,
    },
    SpaceUpdate {
        name: Option<String>,
        accent: Option<String>,
        idempotency_key: Uuid,
    },
    AgentSuspend {
        agent_member_id: Uuid,
        cancel_now: bool,
        idempotency_key: Uuid,
    },
    AgentResume {
        agent_member_id: Uuid,
        idempotency_key: Uuid,
    },
    AuditList {
        before: Option<Uuid>,
        limit: i64,
    },
}

impl AgentAction {
    pub fn name(&self) -> &'static str {
        match self {
            Self::MemberList { .. } => "member.list",
            Self::ChannelList => "channel.list",
            Self::InboxCurrent => "inbox.current",
            Self::InboxShow { .. } => "inbox.show",
            Self::ChannelRead { .. } => "channel.read",
            Self::ChannelCreate { .. } => "channel.create",
            Self::ChannelMemberAdd { .. } => "channel.member.add",
            Self::ChannelMemberRemove { .. } => "channel.member.remove",
            Self::ChannelArchive { .. } => "channel.archive",
            Self::ThreadRead { .. } => "thread.read",
            Self::MessageSend { .. } => "message.send",
            Self::InboxAck { .. } => "inbox.ack",
            Self::InboxDefer { .. } => "inbox.defer",
            Self::AttachmentUpload { .. } => "attachment.upload",
            Self::AttachmentDownload { .. } => "attachment.download",
            Self::AttachmentInfo { .. } => "attachment.info",
            Self::AgentCreate { .. } => "agent.create",
            Self::SpaceUpdate { .. } => "space.update",
            Self::AgentSuspend { .. } => "agent.suspend",
            Self::AgentResume { .. } => "agent.resume",
            Self::AuditList { .. } => "audit.list",
        }
    }
}

#[derive(Debug, Deserialize, Serialize)]
pub struct LocalResponse {
    pub schema_version: u32,
    pub ok: bool,
    pub data: Option<serde_json::Value>,
    pub error: Option<LocalError>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct AgentIdentity {
    pub run_id: Uuid,
    pub agent_member_id: Uuid,
    pub space_id: Uuid,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct LocalError {
    pub code: String,
    pub message: String,
    pub retryable: bool,
    #[serde(default)]
    pub details: Option<Box<serde_json::Value>>,
}

impl LocalResponse {
    pub fn success<T: Serialize>(data: T) -> Self {
        Self {
            schema_version: 1,
            ok: true,
            data: serde_json::to_value(data).ok(),
            error: None,
        }
    }

    pub fn upstream(data: serde_json::Value) -> Self {
        Self {
            schema_version: 1,
            ok: true,
            data: Some(data),
            error: None,
        }
    }

    pub fn denied() -> Self {
        Self {
            schema_version: 1,
            ok: false,
            data: None,
            error: Some(LocalError {
                code: "permission_denied".to_owned(),
                message: "Agent run capability is invalid or expired".to_owned(),
                retryable: false,
                details: None,
            }),
        }
    }

    pub fn failure(code: impl Into<String>, message: impl Into<String>, retryable: bool) -> Self {
        Self {
            schema_version: 1,
            ok: false,
            data: None,
            error: Some(LocalError {
                code: code.into(),
                message: message.into(),
                retryable,
                details: None,
            }),
        }
    }

    pub fn failure_with_details(
        code: impl Into<String>,
        message: impl Into<String>,
        retryable: bool,
        details: Option<serde_json::Value>,
    ) -> Self {
        let mut response = Self::failure(code, message, retryable);
        if let Some(error) = &mut response.error {
            error.details = details.map(Box::new);
        }
        response
    }
}
