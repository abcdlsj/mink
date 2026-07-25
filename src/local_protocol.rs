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
        limit: i64,
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
}

#[derive(Deserialize, Serialize)]
pub struct LocalResponse {
    pub schema_version: u32,
    pub ok: bool,
    pub data: Option<serde_json::Value>,
    pub error: Option<LocalError>,
}

#[derive(Deserialize, Serialize)]
pub struct AgentIdentity {
    pub run_id: Uuid,
    pub agent_member_id: Uuid,
    pub space_id: Uuid,
}

#[derive(Deserialize, Serialize)]
pub struct LocalError {
    pub code: String,
    pub message: String,
    pub retryable: bool,
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
            }),
        }
    }
}
