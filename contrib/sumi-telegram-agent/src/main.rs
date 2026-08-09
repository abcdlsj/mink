mod config;
mod conversation;
mod markdown;
mod plugin;
mod scheduler;
mod state;
mod telegram;
mod text;
mod types;

use std::{collections::HashMap, sync::Arc, time::Duration};

use anyhow::Context;
use tokio::sync::Mutex;

use crate::{config::Settings, conversation::Conversation, telegram::TelegramClient};

type ConversationRegistry = Arc<Mutex<HashMap<i64, Arc<Mutex<Conversation>>>>>;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    init_tracing();
    let settings = Settings::from_env()?;
    tokio::fs::create_dir_all(&settings.agent_home)
        .await
        .with_context(|| format!("failed to create {}", settings.agent_home.display()))?;
    let client = TelegramClient::new(settings.telegram_token.clone());
    let conversations: ConversationRegistry = Arc::new(Mutex::new(HashMap::new()));
    let mut offset: Option<i64> = None;
    tracing::info!(
        identity = %settings.identity,
        model = %settings.model,
        agent_home = %settings.agent_home.display(),
        "Telegram builtin agent started"
    );
    loop {
        let updates = tokio::select! {
            result = client.get_updates(offset) => match result {
                Ok(updates) => updates,
                Err(error) => {
                    tracing::warn!(%error, "getUpdates failed; retrying");
                    tokio::time::sleep(Duration::from_secs(2)).await;
                    continue;
                }
            },
            _ = tokio::signal::ctrl_c() => {
                tracing::info!("shutting down");
                break;
            }
        };
        let mut tasks = Vec::new();
        let mut next_offset = offset;
        for update in updates {
            next_offset = Some(update.update_id + 1);
            let Some(message) = update.message else {
                continue;
            };
            if message.text.is_none() && message.photo.is_empty() && message.document.is_none() {
                continue;
            }
            let chat_id = message.chat.id;
            let client = client.clone();
            let settings = settings.clone();
            let conversations = conversations.clone();
            let state_path = settings.agent_home.join("conversations.json");
            tasks.push(tokio::spawn(async move {
                let result = handle_update(
                    &settings,
                    &client,
                    &conversations,
                    &state_path,
                    chat_id,
                    message,
                )
                .await;
                if let Err(error) = result {
                    tracing::error!(%chat_id, %error, "failed to handle Telegram message");
                }
            }));
        }
        for task in tasks {
            task.await?;
        }
        offset = next_offset;
    }
    Ok(())
}

async fn handle_update(
    settings: &Settings,
    client: &TelegramClient,
    conversations: &ConversationRegistry,
    state_path: &std::path::Path,
    chat_id: i64,
    message: types::Message,
) -> anyhow::Result<()> {
    let mut registry = conversations.lock().await;
    let conversation = match registry.entry(chat_id) {
        std::collections::hash_map::Entry::Occupied(entry) => entry.get().clone(),
        std::collections::hash_map::Entry::Vacant(entry) => {
            let conversation =
                Conversation::open(settings, chat_id, client.clone(), state_path.to_owned())
                    .await?;
            let conversation = entry.insert(Arc::new(Mutex::new(conversation))).clone();
            spawn_scheduler_loop(conversation.clone());
            conversation
        }
    };
    drop(registry);
    conversation.lock().await.handle_message(&message).await
}

fn spawn_scheduler_loop(conversation: Arc<Mutex<Conversation>>) {
    tokio::spawn(async move {
        loop {
            tokio::time::sleep(Duration::from_secs(5)).await;
            if let Err(error) = conversation.lock().await.handle_due_tasks().await {
                tracing::warn!(%error, "scheduled task delivery failed");
            }
        }
    });
}

fn init_tracing() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();
}
