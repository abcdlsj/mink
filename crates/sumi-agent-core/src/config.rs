use std::{collections::BTreeMap, path::PathBuf};

use secrecy::ExposeSecret;

use crate::provider::ProviderConfig;

/// Sandbox policy for shell tool subprocesses.
#[derive(Clone, Debug, Default)]
pub struct SandboxConfig {
    /// Local capability socket allowed inside the sandbox, if any.
    pub socket_path: Option<PathBuf>,
    /// Runtime executable allowed inside the sandbox; defaults to the current executable.
    pub runtime_executable: Option<PathBuf>,
    /// Extra environment variables injected into sandboxed processes.
    pub environment: BTreeMap<String, String>,
}

/// Configuration for one `AgentRuntime` instance.
#[derive(Clone, Debug)]
pub struct AgentConfig {
    /// Root directory holding `agents/<agent-id>` homes.
    pub computer_home: PathBuf,
    pub provider: ProviderConfig,
    pub sandbox: SandboxConfig,
    pub compaction: CompactionConfig,
}

/// Context compaction policy for the agent runtime.
#[derive(Clone, Debug)]
pub struct CompactionConfig {
    /// Estimated/provided context tokens that trigger a preemptive compaction.
    pub trigger_tokens: usize,
    /// Recent context tokens kept unsummarized after compaction.
    pub keep_recent_tokens: usize,
}

impl Default for CompactionConfig {
    fn default() -> Self {
        Self {
            trigger_tokens: 32_000,
            keep_recent_tokens: 20_000,
        }
    }
}

impl AgentConfig {
    pub fn agent_home(&self, agent_id: uuid::Uuid) -> PathBuf {
        self.computer_home.join("agents").join(agent_id.to_string())
    }

    pub fn validate(&self) -> Result<(), String> {
        if self.provider.model.trim().is_empty() {
            return Err("provider model must not be empty".into());
        }
        if self.provider.api_key.expose_secret().trim().is_empty() {
            return Err("provider api_key must not be empty".into());
        }
        if self.provider.base_url.is_some() {
            let base = self.provider.base_url.as_deref().unwrap_or_default();
            if !base.starts_with("http://") && !base.starts_with("https://") {
                return Err("provider base_url must use http or https".into());
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use std::path::Path;

    use super::*;

    #[test]
    fn validate_rejects_empty_model_and_key_without_exposing_secret() {
        let invalid = AgentConfig {
            computer_home: Path::new("/tmp/none").to_owned(),
            provider: ProviderConfig::openai("secret-value", String::new()),
            sandbox: SandboxConfig::default(),
            compaction: CompactionConfig::default(),
        };
        let error = invalid.validate().unwrap_err();
        assert!(!error.contains("secret-value"));

        let valid = AgentConfig {
            computer_home: Path::new("/tmp/none").to_owned(),
            provider: ProviderConfig::openai("secret-value", "test-model".into()),
            sandbox: SandboxConfig::default(),
            compaction: CompactionConfig::default(),
        };
        assert!(valid.validate().is_ok());
        assert!(!format!("{valid:?}").contains("secret-value"));
    }
}
