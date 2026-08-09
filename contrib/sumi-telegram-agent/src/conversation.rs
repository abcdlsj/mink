use std::{
    path::{Path, PathBuf},
    sync::Arc,
    time::{Duration, Instant},
};

use anyhow::{Context, Result, ensure};
use base64::{Engine, engine::general_purpose::STANDARD as BASE64};
use chrono::{DateTime, Utc};
use chrono_tz::Tz;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sumi_agent_core::{
    AgentConfig, AgentRuntime, Attachment, MemoryFile, ProviderConfig, SandboxConfig, TurnOutcome,
    TurnRequest, agent_rooted_path,
};
use tokio::{
    sync::mpsc,
    time::{MissedTickBehavior, interval},
};
use uuid::Uuid;

use crate::{
    config::Settings,
    markdown::{ReplyImage, render_markdown},
    plugin::TelegramPlugin,
    scheduler::{ScheduledTask, SchedulerPlugin},
    state::{ConversationEntry, ConversationState},
    telegram::{TelegramClient, guess_mime},
    text::sanitize_file_name,
    types::Message,
};

const MAX_FILE_BYTES: usize = 20 * 1024 * 1024;
const CONVERSATION_NAMESPACE: Uuid = Uuid::from_u128(0x6f4d_7a2c_9b1e_4d0f_8c3a_2b5e_7d1f_9a04);
const REACTION_WORKING: &str = "👀";
const REACTION_DONE: &str = "✅";
const REACTION_FAILED: &str = "❌";
const REACTION_TIMED_OUT: &str = "⏰";
const SCHEDULER_TICK_SECONDS: u64 = 5;

/// A queued unit of work for one conversation worker.
#[derive(Debug)]
pub enum Job {
    Message(Message),
}

/// Serializes all work for one conversation so the single provider session is
/// never used concurrently. New user messages are enqueued immediately, so
/// sending is never blocked by a running turn; queued messages are processed in
/// order after the current turn finishes, ahead of scheduled ticks.
pub struct ConversationWorker {
    conversation: Conversation,
    rx: mpsc::UnboundedReceiver<Job>,
}

impl ConversationWorker {
    pub fn spawn(conversation: Conversation) -> mpsc::UnboundedSender<Job> {
        let (tx, rx) = mpsc::unbounded_channel();
        let worker = Self { conversation, rx };
        tokio::spawn(worker.run());
        tx
    }

    async fn run(mut self) {
        let mut tick = interval(std::time::Duration::from_secs(SCHEDULER_TICK_SECONDS));
        tick.set_missed_tick_behavior(MissedTickBehavior::Delay);
        loop {
            // Drains already queued messages before a scheduler tick can fire.
            if let Ok(job) = self.rx.try_recv() {
                if let Err(error) = self.handle(job).await {
                    tracing::warn!(%error, "conversation job failed");
                }
                continue;
            }
            tokio::select! {
                biased;
                job = self.rx.recv() => match job {
                    Some(job) => {
                        if let Err(error) = self.handle(job).await {
                            tracing::warn!(%error, "conversation job failed");
                        }
                    }
                    None => break,
                },
                _ = tick.tick() => {
                    if let Err(error) = self.conversation.handle_due_tasks().await {
                        tracing::warn!(%error, "scheduled task delivery failed");
                    }
                }
            }
        }
    }

    async fn handle(&mut self, job: Job) -> Result<()> {
        match job {
            Job::Message(message) => self.conversation.handle_message(&message).await,
        }
    }
}

struct TelegramFile {
    file_id: String,
    file_unique_id: String,
    declared_size: usize,
    file_name: Option<String>,
    mime_type: Option<String>,
    image: bool,
    fallback_kind: &'static str,
}

pub struct Conversation {
    agent_id: Uuid,
    runtime: AgentRuntime,
    locator: String,
    last_message_id: i64,
    plugin: Arc<TelegramPlugin<TelegramClient>>,
    scheduler_plugin: Arc<SchedulerPlugin>,
    client: TelegramClient,
    chat_id: i64,
    identity: String,
    role: String,
    product_contract: String,
    driver_contract: String,
    turn_timeout: Duration,
    state_path: PathBuf,
}

impl Conversation {
    pub async fn open(
        settings: &Settings,
        chat_id: i64,
        client: TelegramClient,
        state_path: PathBuf,
    ) -> Result<Self> {
        let agent_id = Uuid::new_v5(&CONVERSATION_NAMESPACE, &chat_id.to_le_bytes());
        let config = AgentConfig {
            computer_home: settings.agent_home.clone(),
            provider: ProviderConfig::openai(settings.api_key.clone(), settings.model.clone())
                .with_base_url(settings.api_base.clone()),
            sandbox: SandboxConfig::default(),
            compaction: sumi_agent_core::CompactionConfig::default(),
        };
        let agent_home = config.agent_home(agent_id);
        let plugin = Arc::new(TelegramPlugin::new(chat_id, client.clone()));
        let scheduler_plugin = Arc::new(SchedulerPlugin::new(
            agent_home.join("scheduler.json"),
            settings.timezone,
        ));
        let mut runtime = AgentRuntime::new(config, vec![plugin.clone(), scheduler_plugin.clone()]);
        runtime
            .provision(agent_id, &settings.identity, &settings.role)
            .await?;
        let state = ConversationState::load(&state_path).await?;
        let entry = state
            .conversations
            .get(&chat_id.to_string())
            .cloned()
            .unwrap_or_default();
        let locator = match resume_locator(&mut runtime, agent_id, &entry.locator).await? {
            Some(locator) => locator,
            None => runtime.create_session(agent_id).await?,
        };
        Ok(Self {
            agent_id,
            runtime,
            locator,
            last_message_id: entry.last_message_id,
            plugin,
            scheduler_plugin,
            client,
            chat_id,
            identity: settings.identity.clone(),
            role: settings.role.clone(),
            product_contract: settings.product_contract.clone(),
            driver_contract: settings.driver_contract.clone(),
            turn_timeout: settings.turn_timeout,
            state_path,
        })
    }

    pub async fn handle_message(&mut self, message: &Message) -> Result<()> {
        if message.message_id <= self.last_message_id {
            return Ok(());
        }
        self.last_message_id = message.message_id;
        let text = message.text_content();
        self.plugin.set_reply_target(Some(message.message_id));
        self.scheduler_plugin
            .set_reply_target(Some(message.message_id));
        self.react(REACTION_WORKING).await;
        if text.trim() == "/reset" {
            self.reset(message.message_id).await?;
            self.react(REACTION_DONE).await;
            self.persist().await?;
            return Ok(());
        }
        self.client.send_chat_action(self.chat_id, "typing").await?;
        let (attachments, descriptors) = match self.ingest(message).await {
            Ok(ingested) => ingested,
            Err(error) => {
                tracing::warn!(%error, "failed to ingest Telegram attachment");
                self.react(REACTION_FAILED).await;
                self.client
                    .send_message(
                        self.chat_id,
                        "Sorry, I could not process the attachment.",
                        Some(message.message_id),
                        None,
                    )
                    .await?;
                self.persist().await?;
                return Ok(());
            }
        };
        let run_id = Uuid::now_v7();
        let memory = self.runtime.list_memory(self.agent_id).await?;
        let now = self.local_now().to_rfc3339();
        let request = TurnRequest {
            product_contract: self.product_contract.clone(),
            driver_contract: self.driver_contract.clone(),
            identity: self.identity.clone(),
            role: self.role.clone(),
            input: build_turn_input(message, &descriptors, &memory, &now),
            content_hash: content_hash(&text, &descriptors),
            attachments,
            blocked_tools: Default::default(),
            sandbox_environment: Default::default(),
        };
        if let Err(error) = self
            .runtime
            .start_turn(run_id, &self.locator, request)
            .await
        {
            tracing::warn!(%error, "failed to start agent turn");
            self.react(REACTION_FAILED).await;
            self.client
                .send_message(
                    self.chat_id,
                    "Sorry, an error occurred while starting the request.",
                    Some(message.message_id),
                    None,
                )
                .await?;
            self.persist().await?;
            return Ok(());
        }
        let outcome = self.wait_for_outcome(run_id).await?;
        match outcome {
            TurnOutcome::Completed => {
                self.react(REACTION_DONE).await;
                if let Some(reply) = self.runtime.latest_reply(&self.locator).await? {
                    self.send_reply(&reply, Some(message.message_id)).await?;
                }
            }
            TurnOutcome::Failed => {
                self.react(REACTION_FAILED).await;
                self.client
                    .send_message(
                        self.chat_id,
                        "Sorry, an error occurred while processing your request.",
                        Some(message.message_id),
                        None,
                    )
                    .await?;
            }
            TurnOutcome::Interrupted => {
                self.react(REACTION_TIMED_OUT).await;
                self.client
                    .send_message(
                        self.chat_id,
                        "The request timed out.",
                        Some(message.message_id),
                        None,
                    )
                    .await?;
            }
        }
        self.persist().await
    }

    pub async fn handle_due_tasks(&mut self) -> Result<()> {
        let now = Utc::now().timestamp();
        let due = self.scheduler_plugin.take_due(now).await?;
        for task in due {
            if let Err(error) = self.run_scheduled_task(&task).await {
                tracing::warn!(%error, task_id = %task.id, "scheduled task turn failed");
            }
            if let Err(error) = self.scheduler_plugin.reschedule(&task).await {
                tracing::warn!(%error, task_id = %task.id, "scheduled task reschedule failed");
            }
        }
        Ok(())
    }

    async fn run_scheduled_task(&mut self, task: &ScheduledTask) -> Result<()> {
        let run_id = Uuid::now_v7();
        let memory = self.runtime.list_memory(self.agent_id).await?;
        let request = TurnRequest {
            product_contract: self.product_contract.clone(),
            driver_contract: self.driver_contract.clone(),
            identity: self.identity.clone(),
            role: self.role.clone(),
            input: json!({
                "scheduled_task": {
                    "id": task.id,
                    "prompt": task.prompt,
                    "created_at_unix": task.created_at_unix,
                },
                "conversation": {
                    "platform": "telegram",
                    "chat_id": self.chat_id,
                },
                "memory": memory,
                "now": self.local_now().to_rfc3339(),
            }),
            content_hash: format!("scheduled-{}-{}", task.id, task.next_at_unix),
            attachments: Vec::new(),
            blocked_tools: Default::default(),
            sandbox_environment: Default::default(),
        };
        if let Err(error) = self
            .runtime
            .start_turn(run_id, &self.locator, request)
            .await
        {
            tracing::warn!(%error, task_id = %task.id, "failed to start scheduled task");
            self.send_notice(
                "A scheduled task could not be started; it will be retried next time.",
                task.reply_to_message_id,
            )
            .await?;
            return Ok(());
        }
        let outcome = match self.wait_for_outcome(run_id).await {
            Ok(outcome) => outcome,
            Err(error) => {
                tracing::warn!(%error, task_id = %task.id, "scheduled task outcome failed");
                return Ok(());
            }
        };
        match outcome {
            TurnOutcome::Completed => {
                if let Some(reply) = self.runtime.latest_reply(&self.locator).await? {
                    self.send_reply(&reply, task.reply_to_message_id).await?;
                }
            }
            TurnOutcome::Failed => {
                self.send_notice(
                    "A scheduled task failed; it will be retried next time.",
                    task.reply_to_message_id,
                )
                .await?;
            }
            TurnOutcome::Interrupted => {
                self.send_notice(
                    "A scheduled task timed out; it will be retried next time.",
                    task.reply_to_message_id,
                )
                .await?;
            }
        }
        Ok(())
    }

    fn local_now(&self) -> DateTime<Tz> {
        Utc::now().with_timezone(&self.scheduler_plugin.timezone())
    }

    async fn send_notice(&self, text: &str, reply_to_message_id: Option<i64>) -> Result<()> {
        self.client
            .send_message(self.chat_id, text, reply_to_message_id, None)
            .await
    }

    async fn react(&self, emoji: &str) {
        if let Err(error) = self
            .client
            .set_message_reaction(self.chat_id, self.last_message_id, emoji)
            .await
        {
            tracing::warn!(%error, "failed to set Telegram reaction");
        }
    }

    async fn send_reply(&self, reply: &str, reply_to_message_id: Option<i64>) -> Result<()> {
        let rendered = render_markdown(reply);
        for part in rendered.messages {
            if part.trim().is_empty() {
                continue;
            }
            match self
                .client
                .send_message_html(self.chat_id, &part, reply_to_message_id)
                .await
            {
                Ok(()) => {}
                Err(error) => {
                    tracing::warn!(%error, "HTML reply rejected; falling back to plain text");
                    self.client
                        .send_message(self.chat_id, &part, reply_to_message_id, None)
                        .await?;
                }
            }
        }
        self.send_reply_images(&rendered.images, reply_to_message_id)
            .await
    }

    async fn send_reply_images(
        &self,
        images: &[ReplyImage],
        reply_to_message_id: Option<i64>,
    ) -> Result<()> {
        for image in images {
            if image.url.starts_with("http://") || image.url.starts_with("https://") {
                if let Err(error) = self
                    .client
                    .send_photo_url(
                        self.chat_id,
                        &image.url,
                        &image.alt,
                        reply_to_message_id,
                        Some("HTML"),
                    )
                    .await
                {
                    tracing::warn!(%error, url = %image.url, "failed to send remote inline image");
                }
                continue;
            }
            let (root, relative) = match agent_rooted_path(
                &self.runtime.agent_home(self.agent_id),
                &image.url,
            ) {
                Ok(path) => path,
                Err(error) => {
                    tracing::warn!(%error, path = %image.url, "failed to resolve inline image path");
                    continue;
                }
            };
            let path = root.join(&relative);
            let bytes = match tokio::fs::read(&path).await {
                Ok(bytes) => bytes,
                Err(error) => {
                    tracing::warn!(%error, path = %image.url, "failed to read inline image");
                    continue;
                }
            };
            let file_name = relative
                .file_name()
                .and_then(|name| name.to_str())
                .unwrap_or("image");
            if let Err(error) = self
                .client
                .send_photo(
                    self.chat_id,
                    file_name,
                    bytes,
                    &image.alt,
                    reply_to_message_id,
                    Some("HTML"),
                )
                .await
            {
                tracing::warn!(%error, path = %image.url, "failed to send inline image");
            }
        }
        Ok(())
    }

    async fn ingest(&self, message: &Message) -> Result<(Vec<Attachment>, Vec<Value>)> {
        let mut attachments = Vec::new();
        let mut descriptors = Vec::new();
        if let Some(photo) = message
            .photo
            .iter()
            .max_by_key(|photo| photo.file_size.unwrap_or(0))
        {
            let (attachment, descriptor) = self
                .download_attachment(
                    message.message_id,
                    TelegramFile {
                        file_id: photo.file_id.clone(),
                        file_unique_id: photo.file_unique_id.clone(),
                        declared_size: photo.file_size.unwrap_or(0) as usize,
                        file_name: None,
                        mime_type: None,
                        image: true,
                        fallback_kind: "photo",
                    },
                )
                .await?;
            attachments.push(attachment);
            descriptors.push(descriptor);
        }
        if let Some(document) = &message.document {
            let (attachment, descriptor) = self
                .download_attachment(
                    message.message_id,
                    TelegramFile {
                        file_id: document.file_id.clone(),
                        file_unique_id: document.file_unique_id.clone(),
                        declared_size: document.file_size.unwrap_or(0) as usize,
                        file_name: document.file_name.clone(),
                        mime_type: document.mime_type.clone(),
                        image: false,
                        fallback_kind: "document",
                    },
                )
                .await?;
            attachments.push(attachment);
            descriptors.push(descriptor);
        }
        Ok((attachments, descriptors))
    }

    async fn download_attachment(
        &self,
        message_id: i64,
        file: TelegramFile,
    ) -> Result<(Attachment, Value)> {
        ensure!(
            file.declared_size <= MAX_FILE_BYTES,
            "file exceeds the 20 MiB download limit"
        );
        let remote = self.client.get_file(&file.file_id).await?;
        ensure!(
            remote.file_size.unwrap_or(file.declared_size as i64) as usize <= MAX_FILE_BYTES,
            "file exceeds the 20 MiB download limit"
        );
        let file_path = remote
            .file_path
            .as_deref()
            .context("Telegram returned no file path")?;
        let bytes = self.client.download_file(file_path).await?;
        ensure!(
            bytes.len() <= MAX_FILE_BYTES,
            "file exceeds the 20 MiB download limit"
        );
        let fallback = format!("{}_{}", file.fallback_kind, file.file_unique_id);
        let name = sanitize_file_name(
            file.file_name
                .as_deref()
                .or_else(|| {
                    Path::new(file_path)
                        .file_name()
                        .and_then(|name| name.to_str())
                })
                .unwrap_or(&fallback),
        );
        let name = if name.contains('.') || !file.image {
            name
        } else {
            format!("{name}.jpg")
        };
        let relative = format!("workspace/attachments/{message_id}/{name}");
        let target = self
            .runtime
            .agent_home(self.agent_id)
            .join("workspace/attachments")
            .join(message_id.to_string())
            .join(&name);
        tokio::fs::create_dir_all(target.parent().unwrap()).await?;
        tokio::fs::write(&target, &bytes).await?;
        let mime = file
            .mime_type
            .filter(|mime| !mime.is_empty())
            .unwrap_or_else(|| guess_mime(&name).to_owned());
        let attachment = Attachment {
            kind: if file.image { "image" } else { "file" }.into(),
            label: if file.image { "photo" } else { "document" }.into(),
            name: name.clone(),
            mime: mime.to_owned(),
            data: if file.image {
                BASE64.encode(&bytes)
            } else {
                String::new()
            },
            url: String::new(),
        };
        let descriptor = json!({
            "kind": if file.image { "image" } else { "file" },
            "name": name,
            "mime": mime,
            "path": relative,
            "size": bytes.len(),
        });
        Ok((attachment, descriptor))
    }

    async fn wait_for_outcome(&mut self, run_id: Uuid) -> Result<TurnOutcome> {
        let deadline = Instant::now() + self.turn_timeout;
        loop {
            let completions = self.runtime.poll_completions().await?;
            if let Some(completion) = completions
                .into_iter()
                .find(|completion| completion.run_id == run_id)
            {
                return Ok(completion.outcome);
            }
            if Instant::now() >= deadline {
                self.runtime.interrupt(&self.locator).await?;
                loop {
                    let completions = self.runtime.poll_completions().await?;
                    if let Some(completion) = completions
                        .into_iter()
                        .find(|completion| completion.run_id == run_id)
                    {
                        return Ok(completion.outcome);
                    }
                    tokio::task::yield_now().await;
                }
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    }

    async fn reset(&mut self, reply_to_message_id: i64) -> Result<()> {
        let _ = self.runtime.delete_session(&self.locator).await;
        self.locator = self.runtime.create_session(self.agent_id).await?;
        self.client
            .send_message(
                self.chat_id,
                "Conversation reset. Starting fresh.",
                Some(reply_to_message_id),
                None,
            )
            .await
    }

    async fn persist(&self) -> Result<()> {
        let mut state = ConversationState::load(&self.state_path).await?;
        state.conversations.insert(
            self.chat_id.to_string(),
            ConversationEntry {
                locator: self.locator.clone(),
                last_message_id: self.last_message_id,
            },
        );
        state.save(&self.state_path).await
    }
}

async fn resume_locator(
    runtime: &mut AgentRuntime,
    agent_id: Uuid,
    locator: &str,
) -> Result<Option<String>> {
    if locator.is_empty() {
        return Ok(None);
    }
    match runtime.resume_session(agent_id, locator).await {
        Ok(true) => Ok(Some(locator.to_owned())),
        Ok(false) | Err(_) => Ok(None),
    }
}

fn build_turn_input(
    message: &Message,
    descriptors: &[Value],
    memory: &[MemoryFile],
    now: &str,
) -> Value {
    let from = message.from.as_ref();
    let first_name = from
        .and_then(|user| user.first_name.clone())
        .or_else(|| message.chat.first_name.clone());
    let username = from
        .and_then(|user| user.username.clone())
        .or_else(|| message.chat.username.clone());
    let sender = json!({
        "id": from.map(|user| user.id).unwrap_or(message.chat.id),
        "first_name": first_name,
        "username": username,
    });
    json!({
        "conversation": {
            "platform": "telegram",
            "chat_id": message.chat.id,
            "message_id": message.message_id,
            "sender": sender,
            "text": message.text_content(),
            "date": message.date,
        },
        "attachments": descriptors,
        "memory": memory,
        "now": now,
    })
}

fn content_hash(text: &str, descriptors: &[Value]) -> String {
    let mut digest = Sha256::new();
    digest.update(text.as_bytes());
    for descriptor in descriptors {
        digest.update(serde_json::to_vec(descriptor).unwrap_or_default());
    }
    hex::encode(digest.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn turn_input_carries_platform_sender_and_attachment_descriptors() {
        let message = Message {
            message_id: 7,
            chat: crate::types::Chat {
                id: 42,
                first_name: Some("Alice".into()),
                username: None,
            },
            from: Some(crate::types::User {
                id: 42,
                first_name: Some("Alice".into()),
                username: Some("alice".into()),
            }),
            text: Some("hello".into()),
            caption: None,
            photo: Vec::new(),
            document: None,
            date: 123,
        };
        let input = build_turn_input(
            &message,
            &[
                json!({"kind":"file","name":"a.pdf","mime":"application/pdf","path":"workspace/attachments/7/a.pdf","size":3}),
            ],
            &[],
            "2026-08-09T09:00:00+08:00",
        );
        assert_eq!(input["conversation"]["platform"], "telegram");
        assert_eq!(input["conversation"]["chat_id"], 42);
        assert_eq!(input["conversation"]["sender"]["username"], "alice");
        assert_eq!(input["conversation"]["text"], "hello");
        assert_eq!(
            input["attachments"][0]["path"],
            "workspace/attachments/7/a.pdf"
        );
        assert_eq!(input["memory"], json!([]));
        assert_eq!(input["now"], "2026-08-09T09:00:00+08:00");
    }

    #[test]
    fn content_hash_changes_with_text_or_descriptors() {
        let descriptor = json!({"kind":"file","path":"workspace/attachments/1/a.txt"});
        let base = content_hash("hello", std::slice::from_ref(&descriptor));
        assert_ne!(
            base,
            content_hash("hello!", std::slice::from_ref(&descriptor))
        );
        assert_ne!(base, content_hash("hello", &[]));
        assert_eq!(
            base,
            content_hash("hello", std::slice::from_ref(&descriptor))
        );
    }

    #[tokio::test]
    async fn missing_resume_locator_starts_a_fresh_session() {
        let directory = tempfile::tempdir().unwrap();
        let settings = Settings {
            telegram_token: secrecy::SecretString::from("token"),
            api_base: "https://api.openai.com/v1".into(),
            model: "test-model".into(),
            api_key: secrecy::SecretString::from("key"),
            agent_home: directory.path().join("data"),
            identity: "Telegram Agent".into(),
            role: "Role".into(),
            product_contract: "p".into(),
            driver_contract: "d".into(),
            timezone: chrono_tz::Tz::Asia__Shanghai,
            turn_timeout: Duration::from_secs(1),
        };
        let conversation = Conversation::open(
            &settings,
            42,
            TelegramClient::new(secrecy::SecretString::from("bot-token")),
            settings.agent_home.join("conversations.json"),
        )
        .await
        .unwrap();
        assert_eq!(
            conversation.agent_id,
            Uuid::new_v5(&CONVERSATION_NAMESPACE, &42_i64.to_le_bytes())
        );
        assert!(!conversation.locator.is_empty());
        assert!(
            conversation
                .runtime
                .agent_home(conversation.agent_id)
                .join("memory/MEMORY.md")
                .is_file()
        );
    }
}
