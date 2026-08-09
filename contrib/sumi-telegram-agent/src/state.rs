use std::{collections::BTreeMap, path::Path};

use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct ConversationState {
    #[serde(default)]
    pub conversations: BTreeMap<String, ConversationEntry>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct ConversationEntry {
    pub locator: String,
    #[serde(default)]
    pub last_message_id: i64,
}

impl ConversationState {
    pub async fn load(path: &Path) -> Result<Self> {
        let bytes = match tokio::fs::read(path).await {
            Ok(bytes) => bytes,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                return Ok(Self::default());
            }
            Err(error) => {
                return Err(error).with_context(|| format!("failed to read {}", path.display()));
            }
        };
        serde_json::from_slice(&bytes)
            .with_context(|| format!("invalid conversation state at {}", path.display()))
    }

    pub async fn save(&self, path: &Path) -> Result<()> {
        let encoded =
            serde_json::to_vec_pretty(self).context("failed to encode conversation state")?;
        let temporary = path.with_extension(format!("{}.tmp", Uuid::now_v7()));
        tokio::fs::write(&temporary, &encoded)
            .await
            .with_context(|| format!("failed to write {}", temporary.display()))?;
        tokio::fs::rename(&temporary, path)
            .await
            .with_context(|| format!("failed to replace {}", path.display()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn state_round_trips_and_missing_files_default() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("conversations.json");
        assert!(
            ConversationState::load(&path)
                .await
                .unwrap()
                .conversations
                .is_empty()
        );

        let mut state = ConversationState::default();
        state.conversations.insert(
            "123".into(),
            ConversationEntry {
                locator: "locator-1".into(),
                last_message_id: 42,
            },
        );
        state.save(&path).await.unwrap();

        let loaded = ConversationState::load(&path).await.unwrap();
        let entry = &loaded.conversations["123"];
        assert_eq!(entry.locator, "locator-1");
        assert_eq!(entry.last_message_id, 42);
    }
}
