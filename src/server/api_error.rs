use axum::{
    Json,
    http::{StatusCode, header},
    response::IntoResponse,
};
use serde::Serialize;

#[derive(Debug)]
pub enum ApiError {
    Validation {
        code: &'static str,
        message: String,
    },
    Unauthorized,
    InvalidCredentials,
    Forbidden {
        code: &'static str,
        message: String,
    },
    NotFound {
        code: &'static str,
        message: String,
    },
    Conflict {
        code: &'static str,
        message: String,
        details: Option<serde_json::Value>,
    },
    Gone {
        code: &'static str,
        message: String,
    },
    RateLimited,
    Internal,
}

impl ApiError {
    pub fn validation(code: &'static str, message: impl Into<String>) -> Self {
        Self::Validation {
            code,
            message: message.into(),
        }
    }

    pub fn conflict(code: &'static str, message: impl Into<String>) -> Self {
        Self::Conflict {
            code,
            message: message.into(),
            details: None,
        }
    }

    pub fn conflict_with_details(
        code: &'static str,
        message: impl Into<String>,
        details: serde_json::Value,
    ) -> Self {
        Self::Conflict {
            code,
            message: message.into(),
            details: Some(details),
        }
    }

    pub fn forbidden(code: &'static str, message: impl Into<String>) -> Self {
        Self::Forbidden {
            code,
            message: message.into(),
        }
    }

    pub fn not_found(code: &'static str, message: impl Into<String>) -> Self {
        Self::NotFound {
            code,
            message: message.into(),
        }
    }

    pub fn gone(code: &'static str, message: impl Into<String>) -> Self {
        Self::Gone {
            code,
            message: message.into(),
        }
    }

    pub fn database(error: sqlx::Error) -> Self {
        tracing::error!(error = %error, "database operation failed");
        Self::Internal
    }
}

#[derive(Serialize, utoipa::ToSchema)]
pub struct ErrorEnvelope {
    error: ErrorBody,
}

#[derive(Serialize, utoipa::ToSchema)]
pub struct ErrorBody {
    code: &'static str,
    message: String,
    details: Option<serde_json::Value>,
}

impl IntoResponse for ApiError {
    fn into_response(self) -> axum::response::Response {
        let (status, code, message, details, retry_after) = match self {
            Self::Validation { code, message } => {
                (StatusCode::BAD_REQUEST, code, message, None, false)
            }
            Self::Unauthorized => (
                StatusCode::UNAUTHORIZED,
                "unauthorized",
                "Authentication is required".to_owned(),
                None,
                false,
            ),
            Self::InvalidCredentials => (
                StatusCode::UNAUTHORIZED,
                "invalid_credentials",
                "Email or password is incorrect".to_owned(),
                None,
                false,
            ),
            Self::Forbidden { code, message } => {
                (StatusCode::FORBIDDEN, code, message, None, false)
            }
            Self::NotFound { code, message } => (StatusCode::NOT_FOUND, code, message, None, false),
            Self::Conflict {
                code,
                message,
                details,
            } => (StatusCode::CONFLICT, code, message, details, false),
            Self::Gone { code, message } => (StatusCode::GONE, code, message, None, false),
            Self::RateLimited => (
                StatusCode::TOO_MANY_REQUESTS,
                "rate_limited",
                "Too many attempts; retry later".to_owned(),
                None,
                true,
            ),
            Self::Internal => (
                StatusCode::INTERNAL_SERVER_ERROR,
                "internal_error",
                "The request could not be completed".to_owned(),
                None,
                false,
            ),
        };

        let mut response = (
            status,
            Json(ErrorEnvelope {
                error: ErrorBody {
                    code,
                    message,
                    details,
                },
            }),
        )
            .into_response();
        if retry_after {
            response
                .headers_mut()
                .insert(header::RETRY_AFTER, header::HeaderValue::from_static("60"));
        }
        response
    }
}
