use std::{collections::BTreeMap, path::Path};

use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use tokio::sync::Mutex;
use uuid::Uuid;

// Conversation state is shared by all chat workers in one process.  Keep the
// whole read/modify/write sequence behind one lock so two chats cannot replace
// each other's entries with stale snapshots.
static STATE_IO_LOCK: Mutex<()> = Mutex::const_new(());

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
        let _guard = STATE_IO_LOCK.lock().await;
        Self::load_unlocked(path).await
    }

    /// Merge this snapshot into the current file under the process-wide lock.
    /// Callers that loaded before another worker saved therefore retain both
    /// workers' chat entries instead of losing the later update.
    #[cfg(test)]
    async fn save(&self, path: &Path) -> Result<()> {
        let _guard = STATE_IO_LOCK.lock().await;
        let mut state = Self::load_unlocked(path).await?;
        state.conversations.extend(self.conversations.clone());
        state.save_unlocked(path).await
    }

    pub async fn upsert(path: &Path, chat_id: String, entry: ConversationEntry) -> Result<()> {
        let _guard = STATE_IO_LOCK.lock().await;
        let mut state = Self::load_unlocked(path).await?;
        state.conversations.insert(chat_id, entry);
        state.save_unlocked(path).await
    }

    async fn load_unlocked(path: &Path) -> Result<Self> {
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

    async fn save_unlocked(&self, path: &Path) -> Result<()> {
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

    #[tokio::test]
    async fn concurrent_snapshots_merge_chat_entries_without_lost_updates() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("conversations.json");
        let first = ConversationState {
            conversations: BTreeMap::from([(
                "chat-a".into(),
                ConversationEntry {
                    locator: "locator-a".into(),
                    last_message_id: 1,
                },
            )]),
        };
        let second = ConversationState {
            conversations: BTreeMap::from([(
                "chat-b".into(),
                ConversationEntry {
                    locator: "locator-b".into(),
                    last_message_id: 2,
                },
            )]),
        };

        let (first_result, second_result) = tokio::join!(first.save(&path), second.save(&path));
        first_result.unwrap();
        second_result.unwrap();

        let loaded = ConversationState::load(&path).await.unwrap();
        assert_eq!(loaded.conversations.len(), 2);
        assert_eq!(loaded.conversations["chat-a"].last_message_id, 1);
        assert_eq!(loaded.conversations["chat-b"].last_message_id, 2);
    }
}
