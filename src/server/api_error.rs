use axum::{
    Json,
    http::{StatusCode, header},
    response::IntoResponse,
};
use serde::Serialize;

#[derive(Debug)]
pub enum ApiError {
    Validation { code: &'static str, message: String },
    Unauthorized,
    InvalidCredentials,
    Forbidden { code: &'static str, message: String },
    NotFound { code: &'static str, message: String },
    Conflict { code: &'static str, message: String },
    Gone { code: &'static str, message: String },
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

#[derive(Serialize)]
struct ErrorEnvelope {
    error: ErrorBody,
}

#[derive(Serialize)]
struct ErrorBody {
    code: &'static str,
    message: String,
}

impl IntoResponse for ApiError {
    fn into_response(self) -> axum::response::Response {
        let (status, code, message, retry_after) = match self {
            Self::Validation { code, message } => (StatusCode::BAD_REQUEST, code, message, false),
            Self::Unauthorized => (
                StatusCode::UNAUTHORIZED,
                "unauthorized",
                "Authentication is required".to_owned(),
                false,
            ),
            Self::InvalidCredentials => (
                StatusCode::UNAUTHORIZED,
                "invalid_credentials",
                "Email or password is incorrect".to_owned(),
                false,
            ),
            Self::Forbidden { code, message } => (StatusCode::FORBIDDEN, code, message, false),
            Self::NotFound { code, message } => (StatusCode::NOT_FOUND, code, message, false),
            Self::Conflict { code, message } => (StatusCode::CONFLICT, code, message, false),
            Self::Gone { code, message } => (StatusCode::GONE, code, message, false),
            Self::RateLimited => (
                StatusCode::TOO_MANY_REQUESTS,
                "rate_limited",
                "Too many attempts; retry later".to_owned(),
                true,
            ),
            Self::Internal => (
                StatusCode::INTERNAL_SERVER_ERROR,
                "internal_error",
                "The request could not be completed".to_owned(),
                false,
            ),
        };

        let mut response = (
            status,
            Json(ErrorEnvelope {
                error: ErrorBody { code, message },
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
