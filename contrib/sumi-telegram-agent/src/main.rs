mod config;
mod context;
mod conversation;
mod markdown;
mod plugin;
mod scheduler;
mod state;
mod telegram;
mod text;
mod types;

use std::{
    collections::{HashMap, HashSet},
    sync::Arc,
    time::Duration,
};

use anyhow::Context;
use tokio::sync::{Mutex, mpsc, oneshot};

use crate::{
    config::Settings,
    conversation::{Conversation, ConversationWorker, Job},
    telegram::TelegramClient,
};
use teloxide::types::UpdateKind;

type ConversationRegistry = Arc<Mutex<HashMap<i64, mpsc::UnboundedSender<Job>>>>;
type UpdateReceipt = oneshot::Receiver<anyhow::Result<()>>;

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
        let mut receipts = Vec::new();
        let mut dispatch_failed = false;
        let mut blocked_chats = HashSet::new();
        let mut next_offset = offset;
        for update in updates {
            let update_id = update.id.0 as i64;
            next_offset = Some(update_id + 1);
            let UpdateKind::Message(message) = update.kind else {
                continue;
            };
            if message.text().is_none() && message.photo().is_none() && message.document().is_none()
            {
                continue;
            }
            let chat_id = message.chat.id.0;
            if blocked_chats.contains(&chat_id) {
                dispatch_failed = true;
                continue;
            }
            let state_path = settings.agent_home.join("conversations.json");
            match handle_update(
                &settings,
                &client,
                &conversations,
                &state_path,
                chat_id,
                message,
            )
            .await
            {
                Ok(receipt) => receipts.push((update_id, receipt)),
                Err(error) => {
                    dispatch_failed = true;
                    blocked_chats.insert(chat_id);
                    tracing::error!(%chat_id, %error, "failed to enqueue Telegram message");
                }
            }
        }
        let mut batch_succeeded = !dispatch_failed;
        for (update_id, receipt) in receipts {
            match receipt.await {
                Ok(Ok(())) => {}
                Ok(Err(error)) => {
                    batch_succeeded = false;
                    tracing::error!(%update_id, %error, "Telegram message was not durably handled");
                }
                Err(_) => {
                    batch_succeeded = false;
                    tracing::error!(%update_id, "Telegram conversation worker stopped before completion");
                }
            }
        }
        if batch_succeeded {
            // Telegram confirms all updates below this offset only when the
            // next getUpdates request is made. Every receipt above represents
            // a worker completion after its conversation state was persisted.
            offset = next_offset;
        } else {
            // Keep the previous offset so Telegram redelivers any update whose
            // worker did not complete. Persisted message watermarks make
            // already completed updates idempotent on the retry.
            tokio::time::sleep(Duration::from_secs(2)).await;
        }
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
) -> anyhow::Result<UpdateReceipt> {
    let mut registry = conversations.lock().await;
    let sender = match registry.entry(chat_id) {
        std::collections::hash_map::Entry::Occupied(entry) => entry.get().clone(),
        std::collections::hash_map::Entry::Vacant(entry) => {
            let conversation =
                Conversation::open(settings, chat_id, client.clone(), state_path.to_owned())
                    .await?;
            let sender = ConversationWorker::spawn(conversation);
            entry.insert(sender.clone());
            sender
        }
    };
    drop(registry);
    let (completion, receipt) = oneshot::channel();
    if sender
        .send(Job::Message {
            message,
            completion,
        })
        .is_err()
    {
        conversations.lock().await.remove(&chat_id);
        return Err(anyhow::anyhow!("conversation worker stopped"));
    }
    Ok(receipt)
}

fn init_tracing() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();
}
