use axum::{
    Json,
    http::StatusCode,
    response::{IntoResponse, Response},
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::{
    ids::{ComputerId, IdempotencyKey, MemberId, MessageId, TaskId, ThreadId},
    protocol::computer::{ItemDisposition, RunResult, RunTerminalStatus},
    server::application::{
        execution::{CompleteRun, CompleteRunInput, ItemDispositionInput},
        ports::{ApplicationError, TransactionPort},
        task::{CreateTaskFromRootMessage, CreateTaskInput, TaskSource},
    },
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(super) enum AuthenticationSurface {
    Browser,
    #[cfg(test)]
    Computer,
    #[cfg(test)]
    Agent,
}

#[derive(Debug, Eq, PartialEq)]
pub(super) struct WriteContext {
    pub(super) authentication: AuthenticationSurface,
    pub(super) idempotency_key: IdempotencyKey,
}

#[derive(Serialize)]
struct ErrorBody {
    error: ErrorDetail,
}

#[derive(Serialize)]
struct ErrorDetail {
    code: &'static str,
    message: &'static str,
    retryable: bool,
}

pub(super) struct HttpError(ApplicationError);

#[derive(Clone, Copy)]
pub(super) struct BrowserPrincipal {
    pub(super) member_id: MemberId,
}

#[derive(Clone, Copy)]
pub(super) struct ComputerPrincipal {
    pub(super) computer_id: ComputerId,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CreateTaskBody {
    pub(super) title: String,
    pub(super) assignee_agent_member_id: Option<MemberId>,
}

#[derive(Serialize)]
pub(super) struct CreatedTask {
    pub(super) id: TaskId,
    source_thread_id: ThreadId,
    status: &'static str,
}

impl From<ApplicationError> for HttpError {
    fn from(value: ApplicationError) -> Self {
        Self(value)
    }
}

impl IntoResponse for HttpError {
    fn into_response(self) -> Response {
        let (status, code, message, retryable) = match self.0 {
            ApplicationError::Domain(_) | ApplicationError::Conflict => (
                StatusCode::CONFLICT,
                "conflict",
                "request conflicts with current state",
                false,
            ),
            ApplicationError::NotFound => (
                StatusCode::NOT_FOUND,
                "not_found",
                "resource was not found",
                false,
            ),
            ApplicationError::Unauthenticated => (
                StatusCode::UNAUTHORIZED,
                "unauthenticated",
                "credential is missing or expired",
                false,
            ),
            ApplicationError::PayloadTooLarge => (
                StatusCode::BAD_REQUEST,
                "invalid_argument",
                "request payload exceeds the configured limit",
                false,
            ),
            ApplicationError::PermissionDenied => (
                StatusCode::FORBIDDEN,
                "permission_denied",
                "actor is not allowed to perform this action",
                false,
            ),
            ApplicationError::ContextChanged => (
                StatusCode::CONFLICT,
                "context_changed",
                "run context changed",
                true,
            ),
            ApplicationError::Unavailable => (
                StatusCode::SERVICE_UNAVAILABLE,
                "unavailable",
                "external dependency is unavailable",
                true,
            ),
            ApplicationError::Internal => (
                StatusCode::INTERNAL_SERVER_ERROR,
                "internal",
                "server adapter failed",
                false,
            ),
        };
        (
            status,
            Json(ErrorBody {
                error: ErrorDetail {
                    code,
                    message,
                    retryable,
                },
            }),
        )
            .into_response()
    }
}

pub(super) fn write_context(
    expected: AuthenticationSurface,
    authenticated_as: AuthenticationSurface,
    idempotency_key: Option<&str>,
) -> Result<WriteContext, HttpError> {
    if expected != authenticated_as {
        return Err(ApplicationError::PermissionDenied.into());
    }
    let key = idempotency_key
        .ok_or(ApplicationError::Conflict)?
        .parse()
        .map_err(|_| ApplicationError::Conflict)?;
    Ok(WriteContext {
        authentication: authenticated_as,
        idempotency_key: key,
    })
}

pub(super) async fn create_task<P: TransactionPort + Clone>(
    port: &P,
    principal: BrowserPrincipal,
    root_message_id: MessageId,
    context: WriteContext,
    body: CreateTaskBody,
) -> Result<Json<CreatedTask>, HttpError> {
    if context.authentication != AuthenticationSurface::Browser {
        return Err(ApplicationError::PermissionDenied.into());
    }
    let mut port = port.clone();
    let task = CreateTaskFromRootMessage::execute(
        &mut port,
        CreateTaskInput {
            task_id: TaskId::from_uuid(Uuid::now_v7()),
            actor_member_id: principal.member_id,
            source: TaskSource::HumanRoot(ThreadId::from_uuid(root_message_id.into_uuid())),
            title: body.title,
            assignee_agent_member_id: body.assignee_agent_member_id,
            idempotency_key: context.idempotency_key,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await?;
    let status = match task.status {
        crate::server::domain::task::TaskStatus::Todo => "todo",
        crate::server::domain::task::TaskStatus::InProgress => "in_progress",
        crate::server::domain::task::TaskStatus::InReview => "in_review",
        crate::server::domain::task::TaskStatus::Done => "done",
        crate::server::domain::task::TaskStatus::Closed => "closed",
    };
    Ok(Json(CreatedTask {
        id: task.id,
        source_thread_id: task.source_thread_id,
        status,
    }))
}

pub(super) async fn submit_run_result<P: TransactionPort + Clone>(
    port: &P,
    principal: ComputerPrincipal,
    result: RunResult,
) -> Result<StatusCode, HttpError> {
    let token_hash = hex::encode(Sha256::digest(result.fencing_token.expose().as_bytes()));
    let outcome = match result.status {
        RunTerminalStatus::Completed => crate::server::domain::execution::RunOutcome::Completed,
        RunTerminalStatus::Yielded => crate::server::domain::execution::RunOutcome::Yielded,
        RunTerminalStatus::Failed => crate::server::domain::execution::RunOutcome::Failed,
        RunTerminalStatus::Canceled => crate::server::domain::execution::RunOutcome::Canceled,
    };
    let error_code = result.error_code.map(run_error_code);
    let item_dispositions = result
        .item_outcomes
        .into_iter()
        .map(|item| ItemDispositionInput {
            item_id: item.item_id,
            disposition: match item.disposition {
                ItemDisposition::Handled => {
                    crate::server::domain::attention::InboxItemDisposition::Handled
                }
                ItemDisposition::Deferred => {
                    crate::server::domain::attention::InboxItemDisposition::Deferred
                }
                ItemDisposition::Released => {
                    crate::server::domain::attention::InboxItemDisposition::Released
                }
            },
        })
        .collect();
    let mut port = port.clone();
    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            event_id: result.event_id,
            run_id: result.run_id,
            computer_id: principal.computer_id,
            fencing_token_hash: token_hash,
            outcome,
            error_code,
            item_dispositions,
            continuation_note: result.continuation_note,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await?;
    Ok(StatusCode::OK)
}

/// 协议错误码到 domain 取值域的翻译。两侧取值一一对应,新增协议变体会在此处暴露为编译错误。
pub(super) fn run_error_code(
    code: crate::protocol::computer::ComputerErrorCode,
) -> crate::server::domain::execution::RunErrorCode {
    use crate::protocol::computer::ComputerErrorCode;
    use crate::server::domain::execution::RunErrorCode;
    match code {
        ComputerErrorCode::InvalidCommand => RunErrorCode::InvalidCommand,
        ComputerErrorCode::AgentUnavailable => RunErrorCode::AgentUnavailable,
        ComputerErrorCode::ProcessLost => RunErrorCode::ProcessLost,
        ComputerErrorCode::SessionLost => RunErrorCode::SessionLost,
        ComputerErrorCode::SandboxUnavailable => RunErrorCode::SandboxUnavailable,
        ComputerErrorCode::DriverUnavailable => RunErrorCode::DriverUnavailable,
        ComputerErrorCode::Internal => RunErrorCode::Internal,
    }
}

#[cfg(test)]
mod tests {
    use axum::body::to_bytes;
    use serde_json::Value;
    use uuid::Uuid;

    use super::*;

    #[tokio::test]
    async fn authentication_surfaces_cannot_be_substituted() {
        let error = write_context(
            AuthenticationSurface::Computer,
            AuthenticationSurface::Browser,
            Some(&Uuid::now_v7().to_string()),
        )
        .unwrap_err()
        .into_response();
        assert_eq!(error.status(), StatusCode::FORBIDDEN);

        let body: Value =
            serde_json::from_slice(&to_bytes(error.into_body(), usize::MAX).await.unwrap())
                .unwrap();
        assert_eq!(body["error"]["code"], "permission_denied");
        assert!(body["error"].get("details").is_none());
    }

    #[test]
    fn every_write_requires_a_valid_idempotency_key() {
        assert!(
            write_context(
                AuthenticationSurface::Agent,
                AuthenticationSurface::Agent,
                None,
            )
            .is_err()
        );
        assert!(
            write_context(
                AuthenticationSurface::Agent,
                AuthenticationSurface::Agent,
                Some("not-a-uuid"),
            )
            .is_err()
        );
    }

    #[test]
    fn task_create_dto_rejects_system_derived_source_fields() {
        let body = serde_json::json!({
            "title": "Task",
            "source_thread_id": Uuid::now_v7()
        });
        assert!(serde_json::from_value::<CreateTaskBody>(body).is_err());
    }
}
