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
            Self::ThreadRead { .. } => "thread.read",
            Self::MessageSend { .. } => "message.send",
            Self::InboxAck { .. } => "inbox.ack",
            Self::InboxDefer { .. } => "inbox.defer",
            Self::AttachmentUpload { .. } => "attachment.upload",
            Self::AttachmentDownload { .. } => "attachment.download",
            Self::AttachmentInfo { .. } => "attachment.info",
            Self::AgentCreate { .. } => "agent.create",
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn channel_actions_use_structured_cursor_and_idempotency_fields() {
        let message_id = Uuid::now_v7();
        let read = serde_json::to_value(AgentAction::ChannelRead {
            address: "#design".to_owned(),
            before: None,
            after: None,
            around: Some(message_id),
            limit: 25,
        })
        .expect("channel read should serialize");
        assert_eq!(read["action"], "channel_read");
        assert_eq!(read["around"], message_id.to_string());

        let key = Uuid::now_v7();
        let create = serde_json::to_value(AgentAction::ChannelCreate {
            slug: "design".to_owned(),
            name: "Design".to_owned(),
            private: true,
            idempotency_key: key,
        })
        .expect("channel create should serialize");
        assert_eq!(create["action"], "channel_create");
        assert_eq!(create["idempotency_key"], key.to_string());
    }

    #[test]
    fn agent_create_uses_structured_payload() {
        let computer_id = Uuid::now_v7();
        let key = Uuid::now_v7();
        let value = serde_json::to_value(AgentAction::AgentCreate {
            name: "Reviewer".to_owned(),
            role_text: "Review changes.".to_owned(),
            computer_id,
            driver_kind: "codex".to_owned(),
            idempotency_key: key,
        })
        .expect("agent create should serialize");
        assert_eq!(value["action"], "agent_create");
        assert_eq!(value["computer_id"], computer_id.to_string());
        assert_eq!(value["idempotency_key"], key.to_string());
        assert_eq!(AgentAction::InboxCurrent.name(), "inbox.current");
        assert_eq!(
            AgentAction::MessageSend {
                address: "@alice".to_owned(),
                body_markdown: "private".to_owned(),
                based_on: None,
                handle_inbox_item_id: None,
                attachment_ids: Vec::new(),
                idempotency_key: key,
            }
            .name(),
            "message.send"
        );
    }
}
