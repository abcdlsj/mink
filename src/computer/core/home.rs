use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

use crate::ids::{AgentId, SpaceId};

use super::session::DriverKind;

/// Memory 文件投影。不含正文:正文只在读取请求的响应中返回。
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct MemoryFile {
    pub(in crate::computer) path: String,
    pub(in crate::computer) size: u64,
    pub(in crate::computer) sha256: String,
    pub(in crate::computer) updated_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) enum LocalAgentState {
    Active,
    Suspended,
    Retired,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct LocalAgent {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) space_id: SpaceId,
    pub(in crate::computer) name: String,
    pub(in crate::computer) handle: String,
    pub(in crate::computer) role_revision: u64,
    pub(in crate::computer) role: String,
    pub(in crate::computer) driver: DriverKind,
    pub(in crate::computer) state: LocalAgentState,
}
