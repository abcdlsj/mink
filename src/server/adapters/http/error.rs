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
            message: "Run context changed before the write; re-read the current context, then retry once",
        }
    }
}

/// Single source of truth for mapping an `ApplicationError` to the HTTP wire
/// contract shared by REST responses and Agent capability responses.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server::adapters) struct ErrorClass {
    pub(in crate::server::adapters) status: StatusCode,
    pub(in crate::server::adapters) code: &'static str,
    pub(in crate::server::adapters) message: &'static str,
    pub(in crate::server::adapters) retryable: bool,
}

pub(in crate::server::adapters) fn classify(
    error: &crate::server::application::ports::ApplicationError,
) -> ErrorClass {
    use crate::server::application::ports::ApplicationError;
    match error {
        ApplicationError::NotFound => ErrorClass {
            status: StatusCode::NOT_FOUND,
            code: "not_found",
            message: "resource was not found",
            retryable: false,
        },
        ApplicationError::Unauthenticated => ErrorClass {
            status: StatusCode::UNAUTHORIZED,
            code: "unauthenticated",
            message: "credential is missing or expired",
            retryable: false,
        },
        ApplicationError::PayloadTooLarge => ErrorClass {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message: "request payload exceeds the configured limit",
            retryable: false,
        },
        ApplicationError::PermissionDenied => ErrorClass {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "actor is not allowed to perform this action",
            retryable: false,
        },
        ApplicationError::Conflict => ErrorClass {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "request conflicts with current state",
            retryable: false,
        },
        ApplicationError::ContextChanged => ErrorClass {
            status: StatusCode::CONFLICT,
            code: "context_changed",
            message: "Run context changed before the write; re-read the current context, then retry once",
            retryable: false,
        },
        ApplicationError::Unavailable => ErrorClass {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "unavailable",
            message: "external dependency is unavailable",
            retryable: true,
        },
        ApplicationError::Internal => ErrorClass {
            status: StatusCode::INTERNAL_SERVER_ERROR,
            code: "internal",
            message: "server adapter failed",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::GovernorRequired) => ErrorClass {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "Space Owner or Admin access is required",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::InvalidCredential) => ErrorClass {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message: "display name, email, and a password of at least 12 characters are required",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::InvalidPairing) => ErrorClass {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message: "Computer pairing request is invalid",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::InvalidChannelSlug) => ErrorClass {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message: "Use 1-32 lowercase ASCII letters or numbers separated by single hyphens for the Channel slug. Use topic for the human-readable description; topic supports Unicode.",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::PairingLapsed) => ErrorClass {
            status: StatusCode::CONFLICT,
            code: "pairing_lapsed",
            message: "Computer pairing expired or was already confirmed",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::InvalidAttachment) => ErrorClass {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message: "Attachment name and media type are required",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::AttachmentContentMismatch) => ErrorClass {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message: "Attachment size or SHA-256 does not match uploaded content",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::AttachmentNotOpen) => ErrorClass {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "Attachment upload is not open",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::AttachmentNotOwned) => ErrorClass {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "actor is not allowed to perform this action",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::AttachmentNotReady) => ErrorClass {
            status: StatusCode::NOT_FOUND,
            code: "not_found",
            message: "resource was not found",
            retryable: false,
        },
        ApplicationError::Domain(DomainError::ComputerHasAgents) => ErrorClass {
            status: StatusCode::CONFLICT,
            code: "computer_has_agents",
            message: "Computer still has assigned Agents",
            retryable: false,
        },
        ApplicationError::Domain(_) => ErrorClass {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "request conflicts with current state",
            retryable: false,
        },
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
    let class = classify(&error);
    ApiError {
        status: class.status,
        code: class.code,
        message: class.message,
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
        let class = classify(&self.0);
        (
            class.status,
            Json(ErrorBody {
                error: ErrorDetail {
                    code: class.code,
                    message: class.message,
                    retryable: class.retryable,
                },
            }),
        )
            .into_response()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::server::application::ports::ApplicationError;

    #[test]
    fn classify_context_changed_is_actionable_and_not_blindly_retryable() {
        let changed = classify(&ApplicationError::ContextChanged);
        assert_eq!(changed.code, "context_changed");
        assert!(!changed.retryable);
        assert!(changed.message.contains("re-read the current context"));
    }

    #[test]
    fn classify_marks_transient_dependencies_retryable() {
        let unavailable = classify(&ApplicationError::Unavailable);
        assert_eq!(unavailable.code, "unavailable");
        assert!(unavailable.retryable);
    }
}
