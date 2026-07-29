use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

pub(crate) const SCHEMA_VERSION: u16 = 1;

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ErrorCode {
    InvalidArgument,
    Unauthenticated,
    PermissionDenied,
    NotFound,
    Conflict,
    ContextChanged,
    RateLimited,
    Unavailable,
    Internal,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Error {
    pub(crate) code: ErrorCode,
    pub(crate) message: String,
    pub(crate) retryable: bool,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub(crate) details: BTreeMap<String, serde_json::Value>,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Response<T> {
    pub(crate) schema_version: u16,
    pub(crate) ok: bool,
    pub(crate) data: Option<T>,
    pub(crate) error: Option<Error>,
}

impl<T> Response<T> {
    pub(crate) fn success(data: T) -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            ok: true,
            data: Some(data),
            error: None,
        }
    }

    pub(crate) fn failure(error: Error) -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            ok: false,
            data: None,
            error: Some(error),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::ErrorCode;

    #[test]
    fn error_code_has_stable_wire_value() {
        assert_eq!(
            serde_json::to_string(&ErrorCode::PermissionDenied).unwrap(),
            "\"permission_denied\""
        );
    }
}
