use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

use crate::ids::{AgentId, SpaceId};

use super::session::DriverKind;

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
    pub(in crate::computer) role_revision: u64,
    pub(in crate::computer) role: String,
    pub(in crate::computer) driver: DriverKind,
    pub(in crate::computer) state: LocalAgentState,
}

impl LocalAgent {
    pub(in crate::computer) const PRIMARY_MEMORY_PATH: &'static str = "MEMORY.md";

    pub(in crate::computer) fn initial_memory_document(&self) -> String {
        format!(
            concat!(
                "# {}\n\n",
                "## Role\n\n",
                "{}\n\n",
                "## Key Knowledge\n\n",
                "<!-- Add `- Read notes/<topic>.md for <scope>` entries here. -->\n",
                "<!-- Cover collaborator preferences, Channels, projects, domains, work history, and other Agents. -->\n\n",
                "## Active Context\n\n",
                "- Current focus: No active work recorded.\n",
                "- Last interaction: No significant interaction recorded.\n",
            ),
            self.name.trim(),
            self.role.trim()
        )
    }
}
