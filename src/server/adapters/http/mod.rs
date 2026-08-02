// HTTP adapter facade and shared request state.
#![allow(unused_imports)]
use std::{
    collections::{BTreeSet, VecDeque},
    convert::Infallible,
    sync::Arc,
};

use anyhow::Context;
use axum::{
    Json, Router,
    body::Bytes,
    extract::{
        DefaultBodyLimit, Path, Query, State, WebSocketUpgrade,
        ws::{Message as WebSocketMessage, WebSocket},
    },
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{
        IntoResponse, Response, Sse,
        sse::{Event as SseEvent, KeepAlive},
    },
    routing::{get, post},
};
use axum_extra::extract::cookie::{Cookie, CookieJar, SameSite};
use futures_util::{StreamExt, stream};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use time::{Duration, OffsetDateTime};
use tower_http::{
    services::{ServeDir, ServeFile},
    trace::TraceLayer,
};
use uuid::Uuid;

use crate::config::ServerConfig;
use crate::{
    ids::{
        AgentId, AttachmentId, ChannelId, ComputerId, IdempotencyKey, InboxItemId, MemberId,
        MessageId, RunId, SpaceId, TaskId, ThreadId,
    },
    protocol::{
        capability,
        computer::{
            ComputerFrame, ComputerHello, ItemDisposition, MemoryQuery, MemoryReadQuery,
            Query as ComputerQuery, QueryErrorCode, QueryResult, RunResult, RunTerminalStatus,
            ServerFrame, SessionContinuityQuery, SessionContinuityState, SessionScope,
        },
    },
    server::{
        application::attachment::{
            AttachmentContent, CompleteUpload, CompleteUploadInput, OpenUpload, OpenUploadInput,
            ReadAttachment, WriteUploadContent, WriteUploadContentInput,
        },
        application::attention::{
            HardItemRoute, MarkInboxItemRead, MarkInboxItemReadInput, ReadMemberInbox,
            RequeueDeadItem, RequeueDeadItemInput, RouteHardItem, RouteHardItemInput,
        },
        application::computer::{
            AuthenticateComputer, BeginPairing, BeginPairingInput, ConfirmPairing,
            ConfirmPairingInput, ListSpaceComputers, ReadPairedComputer, ReadPairing,
            ReadPairingStatus,
        },
        application::conversation::{
            AddChannelAgents, ArchiveChannel, CreateAgent, CreateAgentAction,
            CreateAgentActionInput, CreateAgentInput, CreateChannel, CreateChannelAction,
            CreateChannelActionInput, CreateChannelInput, DeleteMessage, EditMessage,
            EditMessageInput, JoinChannel, ListDirectMessages, OpenDirectMessage,
            OpenDirectMessageInput, PublishMessage, SetThreadSubscription,
        },
        application::task::{
            CreateTaskFromRootMessage, CreateTaskInput, LinkThreadInput, LinkThreadToTask,
            OutcomeMessage, OutcomeRunContext, RecordTaskOutcome, RecordTaskOutcomeInput,
            TaskAction, TaskOutcome, TaskOutcomeScope, TaskPostTarget, TaskSource,
            UnlinkThreadFromTask, UpdateTask, UpdateTaskInput,
        },
        application::{
            execution::{
                AcknowledgeDelivery, AcknowledgeDeliveryInput, AuthorizeRunCapability,
                ClaimNextRun, ClaimRun, ClaimRunInput, CompleteRun, CompleteRunInput,
                ItemDispositionInput, ReadAgentChannel, ReadCurrentAgentRun, ReclaimExpiredLeases,
                ReclaimExpiredLeasesInput, RecordRunItemDisposition, RecordRunItemDispositionInput,
                RenewRun, RenewRunInput, StartRun, StartRunInput,
            },
            identity::{
                AgentLifecycleAction, AuthenticateHuman, AuthenticateHumanInput,
                AuthenticateSession, AuthorizeAgentAccess, AuthorizeAgentGovernance,
                AuthorizeAttachmentAccess, AuthorizeChannelAccess, AuthorizeComputerGovernance,
                AuthorizeSpaceAccess, AuthorizeSpaceGovernance, CloseSession, CreateSpace,
                CreateSpaceInput, DeleteComputer, RegisterHuman, RegisterHumanInput, RetireAgent,
                SetPermission, UpdateAgent, UpdateAgentInput, UpdateMemberAccess,
            },
            invitation::{
                AcceptInvitation, AcceptInvitationInput, InviteHuman, InviteHumanInput,
                ReadInvitation,
            },
            ports::{
                ApplicationError, AuthenticatedHuman, DirectMessageView, InboxItemView, InboxScope,
                InvitationView, MemberKind, MessageDraft, PairedComputer, RawFencingToken,
                RawInvitationToken, RawPairingCode, RawSessionToken, RunCapabilityProof,
                ServerTransaction, SpaceMemberView, TransactionPort,
            },
        },
        domain::{
            access::SessionLifetime,
            attachment::{Attachment, AttachmentStatus as AttachmentStatusKind, DeclaredContent},
            attention::{AttentionPolicy, AttentionStrength, InboxItemKind, InboxItemStatus},
            conversation::ChannelKind,
            identity::{AccessLevel, DriverKind, PermissionAction},
            pairing::ComputerOs as ComputerOsKind,
            task::CloseReason,
        },
    },
};

use super::realtime::space_events;
use super::{
    credential::{
        Argon2Passwords, NumericPairingCodes, RandomInvitationTokens, RandomSessionTokens,
    },
    object_storage::AttachmentObjectStore,
    openapi::{
        AccessLevel as AccessLevelCode, ActionAgentResponse, ActionChannelResponse,
        AgentAccessLevel, AgentActivityResponse, AgentActivityStatus, AgentLifecycle,
        AgentResponse, AgentRuntimeResponse, AttachmentResponse, AttachmentStatus, AttentionConfig,
        AttentionFailureResponse, ChannelKind as ChannelKindCode, ChannelListResponse,
        ChannelMembersResponse, ChannelResponse, CloseReason as CloseReasonCode, ComputerOs,
        ComputerResponse, ComputerStatus, CreatedInvitationResponse, DirectMessageResponse,
        DriverKind as DriverKindCode, InboxItemResponse, InboxKind, InboxPriority, InboxStatus,
        InvitationResponse, LoginResponse, MemberKind as MemberKindCode, MemberResponse,
        MemoryContentResponse, MemoryFileResponse, MessageAuthor, MessageContentResponse,
        MessagePageResponse, MessagePlacement, MessageResponse, MessageTaskRefResponse,
        MessageTaskSummary, ProvisionStatus, RegisterResponse, RunOutcome, RunResponse, RunStatus,
        SessionContinuityResponse, SessionContinuityState as ContinuityStateCode, SpaceResponse,
        TaskResponse, TaskStatus, ThreadReadResponse, ThreadReferenceResponse, ThreadRelation,
        ThreadSubscriptionResponse, UserResponse,
    },
    postgres::PostgresAdapter,
    query::QueryRegistry,
};

const SESSION_COOKIE: &str = "sumi_session";

#[derive(Clone)]
pub(super) struct RuntimeState {
    pub(super) pool: PgPool,
    pub(super) storage: PostgresAdapter,
    pub(super) objects: Arc<AttachmentObjectStore>,
    pub(super) session_lifetime: SessionLifetime,
    pub(super) attachment_max_bytes: u64,
    pub(super) queries: QueryRegistry,
}

mod attachment;
mod computer;
mod conversation;
mod dto;
mod error;
mod execution;
mod identity;
mod task;

#[cfg(test)]
mod tests;

pub(super) use attachment::*;
pub(super) use computer::*;
pub(super) use conversation::*;
pub(super) use dto::*;
pub(super) use error::*;
pub(super) use execution::*;
pub(super) use identity::*;
pub(super) use task::*;

pub(super) fn api_router(state: RuntimeState, attachment_body_limit: usize) -> Router {
    Router::new()
        .route("/health", get(|| async { "ok" }))
        .route("/auth/register", post(register)).route("/auth/login", post(login)).route("/auth/logout", post(logout)).route("/auth/me", get(current_user))
        .route("/computer-pairings", post(begin_pairing)).route("/computer-pairings/{pairing_id}", get(pairing_details)).route("/computer-pairings/{pairing_id}/confirm", post(confirm_pairing)).route("/computer-pairings/{pairing_id}/status", get(pairing_status))
        .route("/computers/{computer_id}/connect", get(connect_computer)).route("/computers/{computer_id}/agents", get(computer_agents)).route("/computers/{computer_id}/runs/claim", post(claim_run)).route("/computers/{computer_id}/runs/{run_id}/started", post(run_started)).route("/computers/{computer_id}/runs/{run_id}/renew", post(renew_run)).route("/computers/{computer_id}/runs/{run_id}/delivery-receipts", post(delivery_receipt)).route("/computers/{computer_id}/runs/{run_id}/result", post(run_result)).route("/computers/{computer_id}/agent-actions", post(agent_action))
        .route("/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/uploads", post(agent_create_upload)).route("/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/content", axum::routing::put(agent_upload_content).layer(DefaultBodyLimit::max(attachment_body_limit))).route("/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/complete", post(agent_complete_upload)).route("/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/download", get(agent_download_attachment))
        .route("/spaces", get(list_spaces).post(create_space)).route("/spaces/by-slug/{slug}", get(space_by_slug)).route("/spaces/{space_id}/events", get(space_events)).route("/spaces/{space_id}/channels", get(list_channels).post(create_channel)).route("/spaces/{space_id}/members", get(list_members)).route("/spaces/{space_id}/members/{member_id}", axum::routing::patch(update_space_member)).route("/channels/{channel_id}/members/me", post(join_channel)).route("/channels/{channel_id}/archive", post(archive_channel)).route("/threads/{thread_id}/subscription", axum::routing::put(follow_thread).delete(unfollow_thread))
        .route("/spaces/{space_id}/invites", post(invite_human)).route("/invites/{invite_token}", get(invitation_details)).route("/invites/{invite_token}/accept", post(accept_invitation)).route("/spaces/{space_id}/computers", get(list_computers)).route("/spaces/{space_id}/dms", get(list_direct_messages).post(open_direct_message)).route("/spaces/{space_id}/agents", get(list_agents).post(create_agent)).route("/agents/{agent_id}", get(get_agent).patch(update_agent).delete(retire_agent)).route("/agents/{agent_id}/runs/current", get(current_agent_run)).route("/agents/{agent_id}/memory/read", post(read_agent_memory))
        .route("/spaces/{space_id}/tasks", get(list_tasks)).route("/tasks/{task_id}", get(get_task).patch(update_task)).route("/tasks/{task_id}/runs", get(task_runs)).route("/root-messages/{message_id}/task", post(create_task)).route("/tasks/{task_id}/threads", post(link_task_thread)).route("/tasks/{task_id}/threads/{thread_id}", axum::routing::delete(unlink_task_thread)).route("/tasks/{task_id}/start", post(start_task)).route("/tasks/{task_id}/submit-review", post(submit_task_review)).route("/tasks/{task_id}/request-changes", post(request_task_changes)).route("/tasks/{task_id}/done", post(complete_task)).route("/tasks/{task_id}/close", post(close_task)).route("/tasks/{task_id}/reset-session", post(reset_task_session))
        .route("/channels/{channel_id}/messages", get(list_messages).post(create_root_message)).route("/channels/{channel_id}/members", get(list_channel_members).post(add_channel_agents)).route("/threads/{thread_id}", get(read_thread)).route("/threads/{thread_id}/messages", post(create_thread_reply)).route("/messages/{message_id}", axum::routing::patch(update_message).delete(delete_message)).route("/members/{member_id}/inbox", get(member_inbox)).route("/inbox-items/{item_id}/read", post(read_inbox_item)).route("/inbox-items/{item_id}/requeue", post(requeue_inbox_item)).route("/members/{member_id}/permissions/{action_code}", axum::routing::put(grant_permission).delete(revoke_permission))
        .route("/attachments/uploads", post(create_upload)).route("/attachments/{attachment_id}/content", axum::routing::put(upload_content).layer(DefaultBodyLimit::max(attachment_body_limit))).route("/attachments/{attachment_id}/complete", post(complete_upload)).route("/attachments/{attachment_id}/download", get(download_attachment)).route("/computers/{computer_id}", axum::routing::delete(delete_computer)).with_state(state)
}
use crate::server::application::ports::{
    AttachmentTransaction, CollaborationTransaction, EffectSink, ExecutionTransaction,
    IdentityTransaction, TaskTransaction,
};
