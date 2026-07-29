#![allow(dead_code)]

use anyhow::Context;
use utoipa::OpenApi;

use crate::agent_config::{AttentionConfig, SuspendMode};

use super::{
    agent_registry, api_error, approval, attachment, auth, channel, computer_pairing, inbox,
    member, message, space, task, thread,
};

#[derive(OpenApi)]
#[openapi(
    info(title = "Sumi Browser API", version = "1"),
    components(schemas(
        auth::RegisterRequest,
        auth::UserResponse,
        auth::RegisterResponse,
        auth::LoginRequest,
        auth::LoginResponse,
        api_error::ErrorEnvelope,
        api_error::ErrorBody,
        space::CreateSpaceRequest,
        space::SpaceResponse,
        computer_pairing::ConfirmPairingRequest,
        computer_pairing::ComputerResponse,
        computer_pairing::PairingDetailsResponse,
        agent_registry::CreateAgentRequest,
        agent_registry::AgentResponse,
        agent_registry::AgentActivityResponse,
        AttentionConfig,
        agent_registry::MemoryFileResponse,
        agent_registry::ReadMemoryRequest,
        agent_registry::MemoryContentResponse,
        agent_registry::UpdateAgentRequest,
        agent_registry::LifecycleAction,
        SuspendMode,
        member::MemberResponse,
        member::UpdateMemberRequest,
        member::CreateInvitationRequest,
        member::InvitationResponse,
        channel::ChannelResponse,
        channel::ChannelMembersResponse,
        channel::ChannelListResponse,
        channel::DirectMessageResponse,
        channel::CreateDirectMessageRequest,
        channel::CreateChannelRequest,
        channel::AddChannelAgentsRequest,
        message::MessageAuthor,
        message::MessageTaskSummary,
        message::MessageResponse,
        message::MessagePageResponse,
        message::CreateMessageRequest,
        attachment::AttachmentResponse,
        attachment::CreateUploadRequest,
        attachment::CompleteUploadRequest,
        inbox::InboxItemResponse,
        inbox::DeferRequest,
        task::TaskResponse,
        approval::AgentCreateApprovalPayload,
        approval::ApprovalResponse,
        thread::CreateThreadRequest,
        thread::ThreadResponse,
        thread::CreateThreadMessageRequest,
        thread::ThreadReadResponse,
        thread::ThreadSubscriptionResponse
    ))
)]
pub struct BrowserApi;

pub fn browser_openapi_json() -> anyhow::Result<String> {
    let mut schema = serde_json::to_value(BrowserApi::openapi())
        .context("failed to build Browser API OpenAPI schema")?;
    for (schema_name, property, values) in [
        (
            "AgentCreateApprovalPayload",
            "access_level",
            &["member"][..],
        ),
        (
            "AgentCreateApprovalPayload",
            "driver_kind",
            &["codex", "builtin"],
        ),
        ("AgentResponse", "access_level", &["member", "admin"]),
        (
            "AgentResponse",
            "activity_status",
            &[
                "idle",
                "queued",
                "starting",
                "running",
                "stopping",
                "unreachable",
                "suspended",
                "error",
            ],
        ),
        (
            "AgentResponse",
            "desired_lifecycle",
            &["active", "suspended", "retired"],
        ),
        ("AgentResponse", "driver_kind", &["codex", "builtin"]),
        (
            "AgentResponse",
            "provision_status",
            &["provisioning", "ready", "error"],
        ),
        (
            "ApprovalResponse",
            "status",
            &["pending", "approved", "rejected", "canceled"],
        ),
        ("ApprovalResponse", "type", &["agent.create"]),
        (
            "AttachmentResponse",
            "status",
            &["uploading", "ready", "deleted"],
        ),
        ("ChannelResponse", "kind", &["public", "private"]),
        ("ComputerResponse", "os", &["macos", "linux"]),
        (
            "ComputerResponse",
            "status",
            &["online", "offline", "revoked"],
        ),
        ("CreateAgentRequest", "access_level", &["member", "admin"]),
        ("CreateAgentRequest", "driver_kind", &["codex", "builtin"]),
        ("CreateChannelRequest", "kind", &["public", "private"]),
        (
            "InboxItemResponse",
            "kind",
            &[
                "direct",
                "mention",
                "reply",
                "thread_activity",
                "channel_activity",
                "approval",
                "system",
            ],
        ),
        ("InboxItemResponse", "priority", &["hard", "ambient"]),
        (
            "InboxItemResponse",
            "status",
            &["pending", "deferred", "handled"],
        ),
        (
            "MemberResponse",
            "access_level",
            &["owner", "admin", "member"],
        ),
        ("MemberResponse", "kind", &["human", "agent"]),
        ("MessageAuthor", "kind", &["human", "agent"]),
        (
            "MessageTaskSummary",
            "status",
            &["open", "in_progress", "done", "canceled"],
        ),
        (
            "PairingDetailsResponse",
            "status",
            &["pending", "confirmed", "expired"],
        ),
        (
            "TaskResponse",
            "status",
            &["open", "in_progress", "done", "canceled"],
        ),
    ] {
        set_string_enum(&mut schema, schema_name, property, values)?;
    }
    for property in ["dm_immediate", "mention_immediate"] {
        let pointer = format!("/components/schemas/AttentionConfig/properties/{property}");
        schema
            .pointer_mut(&pointer)
            .with_context(|| format!("missing schema property {pointer}"))?
            .as_object_mut()
            .context("schema property must be an object")?
            .insert("enum".to_owned(), serde_json::json!([true]));
    }
    serde_json::to_string_pretty(&schema).context("failed to serialize Browser API OpenAPI schema")
}

fn set_string_enum(
    schema: &mut serde_json::Value,
    schema_name: &str,
    property: &str,
    values: &[&str],
) -> anyhow::Result<()> {
    let pointer = format!("/components/schemas/{schema_name}/properties/{property}");
    schema
        .pointer_mut(&pointer)
        .with_context(|| format!("missing schema property {pointer}"))?
        .as_object_mut()
        .context("schema property must be an object")?
        .insert("enum".to_owned(), serde_json::json!(values));
    Ok(())
}

pub fn write_browser_openapi() -> anyhow::Result<()> {
    println!("{}", browser_openapi_json()?);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn browser_schema_contains_every_web_wire_type() {
        let schema: serde_json::Value =
            serde_json::from_str(&browser_openapi_json().expect("schema JSON"))
                .expect("valid JSON");
        let schemas = schema["components"]["schemas"]
            .as_object()
            .expect("schemas");
        for expected in [
            "AgentResponse",
            "AttachmentResponse",
            "ChannelResponse",
            "MessageResponse",
            "SpaceResponse",
            "TaskResponse",
            "ThreadReadResponse",
            "UserResponse",
        ] {
            assert!(schemas.contains_key(expected), "missing {expected}");
        }
    }
}
