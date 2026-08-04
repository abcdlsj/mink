use super::*;
use crate::server::domain::DomainError;
use serde::Serialize;

#[derive(Debug)]
pub(in crate::server::adapters) struct ApiError {
    pub(in crate::server::adapters) status: StatusCode,
    pub(in crate::server::adapters) code: &'static str,
    pub(in crate::server::adapters) message: &'static str,
}

impl ApiError {
    pub(in crate::server::adapters) fn unauthenticated() -> Self {
        Self {
            status: StatusCode::UNAUTHORIZED,
            code: "unauthenticated",
            message: "Browser Session is missing or expired",
        }
    }

    pub(in crate::server::adapters) fn invalid(message: &'static str) -> Self {
        Self {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message,
        }
    }

    pub(in crate::server::adapters) fn permission_denied() -> Self {
        Self {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "Member cannot access this resource",
        }
    }

    pub(in crate::server::adapters) fn not_found() -> Self {
        Self {
            status: StatusCode::NOT_FOUND,
            code: "not_found",
            message: "resource was not found",
        }
    }

    pub(in crate::server::adapters) fn internal() -> Self {
        Self {
            status: StatusCode::INTERNAL_SERVER_ERROR,
            code: "internal",
            message: "Server could not complete the request",
        }
    }

    pub(in crate::server::adapters) fn computer_unreachable() -> Self {
        Self {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "computer_unreachable",
            message: "Computer did not answer the query",
        }
    }

    pub(in crate::server::adapters) fn context_changed() -> Self {
        Self {
            status: StatusCode::CONFLICT,
            code: "context_changed",
            message: "Message context changed before the write",
        }
    }
}

pub(super) fn bearer_token(headers: &HeaderMap) -> Result<&str, ApiError> {
    headers
        .get("Authorization")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .filter(|value| !value.is_empty())
        .ok_or_else(ApiError::unauthenticated)
}

pub(in crate::server::adapters) fn application_error(
    error: crate::server::application::ports::ApplicationError,
) -> ApiError {
    use crate::server::application::ports::ApplicationError;
    match error {
        ApplicationError::NotFound => ApiError::not_found(),
        ApplicationError::Unauthenticated => ApiError::unauthenticated(),
        ApplicationError::Domain(DomainError::GovernorRequired) => ApiError {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "Space Owner or Admin access is required",
        },
        ApplicationError::Domain(DomainError::InvalidCredential) => ApiError::invalid(
            "display name, email, and a password of at least 12 characters are required",
        ),
        ApplicationError::Domain(DomainError::InvalidPairing) => {
            ApiError::invalid("Computer pairing request is invalid")
        }
        ApplicationError::Domain(DomainError::InvalidChannelSlug) => ApiError::invalid(
            "Use 1-32 lowercase ASCII letters or numbers separated by single hyphens for the Channel slug. Use topic for the human-readable description; topic supports Unicode.",
        ),
        ApplicationError::Domain(DomainError::PairingLapsed) => ApiError {
            status: StatusCode::CONFLICT,
            code: "pairing_lapsed",
            message: "Computer pairing expired or was already confirmed",
        },
        ApplicationError::PayloadTooLarge => ApiError::invalid("Attachment is too large"),
        ApplicationError::Domain(DomainError::InvalidAttachment) => {
            ApiError::invalid("Attachment name and media type are required")
        }
        ApplicationError::Domain(DomainError::AttachmentContentMismatch) => {
            ApiError::invalid("Attachment size or SHA-256 does not match uploaded content")
        }
        ApplicationError::Domain(DomainError::AttachmentNotOpen) => ApiError {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "Attachment upload is not open",
        },
        ApplicationError::Domain(DomainError::AttachmentNotOwned) => ApiError::permission_denied(),
        ApplicationError::Domain(DomainError::AttachmentNotReady) => ApiError::not_found(),
        ApplicationError::PermissionDenied => ApiError {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "actor is not allowed to perform this action",
        },
        ApplicationError::ContextChanged => ApiError::context_changed(),
        ApplicationError::Domain(DomainError::ComputerHasAgents) => ApiError {
            status: StatusCode::CONFLICT,
            code: "computer_has_agents",
            message: "Computer still has assigned Agents",
        },
        ApplicationError::Domain(_) | ApplicationError::Conflict => ApiError {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "request conflicts with current state",
        },
        ApplicationError::Unavailable => ApiError {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "unavailable",
            message: "dependency is unavailable",
        },
        ApplicationError::Internal => ApiError::internal(),
    }
}

pub(in crate::server::adapters) fn token_hash(token: &str) -> String {
    hex::encode(Sha256::digest(token.as_bytes()))
}

#[derive(Serialize)]
pub(super) struct ErrorBody {
    error: ErrorDetail,
}

#[derive(Serialize)]
pub(super) struct ErrorDetail {
    code: &'static str,
    message: &'static str,
    retryable: bool,
}

pub(in crate::server::adapters) struct HttpError(ApplicationError);

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(json!({
                "error": {
                    "code": self.code,
                    "message": self.message,
                    "retryable": false
                }
            })),
        )
            .into_response()
    }
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
