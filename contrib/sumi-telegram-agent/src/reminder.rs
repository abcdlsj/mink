use std::{
    path::{Path, PathBuf},
    sync::Mutex,
    time::{SystemTime, UNIX_EPOCH},
};

use anyhow::{Context, Result, bail, ensure};
use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sumi_builtin_agent::{AgentPlugin, PluginContext, ToolDef};
use uuid::Uuid;

use crate::{markdown::render_markdown, telegram::TelegramClient};

const MAX_REMINDER_MINUTES: u64 = 60 * 24 * 30;
const REMINDER_CONTRACT: &str = concat!(
    "Reminder tools: `reminder.set` schedules a one-shot reminder with `text` and `in_minutes` ",
    "(1 to 43200); the bot delivers `⏰ Reminder: <text>` to this chat when it is due. Use ",
    "`reminder.list` to inspect scheduled reminders and `reminder.cancel` with the returned id ",
    "to remove one. Reminders survive restarts.",
);

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Reminder {
    pub id: Uuid,
    pub text: String,
    pub due_at_unix: i64,
    #[serde(default)]
    pub reply_to_message_id: Option<i64>,
    pub created_at_unix: i64,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
struct ReminderState {
    #[serde(default)]
    reminders: Vec<Reminder>,
}

#[async_trait]
pub trait ReminderSender: Send + Sync {
    async fn send_reminder(
        &self,
        chat_id: i64,
        text: &str,
        reply_to_message_id: Option<i64>,
    ) -> Result<()>;
}

#[async_trait]
impl ReminderSender for TelegramClient {
    async fn send_reminder(
        &self,
        chat_id: i64,
        text: &str,
        reply_to_message_id: Option<i64>,
    ) -> Result<()> {
        let rendered = render_markdown(text);
        for part in rendered.messages {
            match self
                .send_message_html(chat_id, &part, reply_to_message_id)
                .await
            {
                Ok(()) => {}
                Err(error) => {
                    tracing::warn!(%error, "HTML reminder rejected; falling back to plain text");
                    self.send_message(chat_id, &part, reply_to_message_id, None)
                        .await?;
                }
            }
        }
        Ok(())
    }
}

pub struct ReminderPlugin<C: ReminderSender> {
    chat_id: i64,
    sender: C,
    state_path: PathBuf,
    reply_to: Mutex<Option<i64>>,
}

impl<C: ReminderSender> ReminderPlugin<C> {
    pub fn new(chat_id: i64, sender: C, state_path: PathBuf) -> Self {
        Self {
            chat_id,
            sender,
            state_path,
            reply_to: Mutex::new(None),
        }
    }

    pub fn set_reply_target(&self, message_id: Option<i64>) {
        *self
            .reply_to
            .lock()
            .expect("reply target lock is not poisoned") = message_id;
    }

    async fn state(&self) -> Result<ReminderState> {
        ReminderState::load(&self.state_path).await
    }

    async fn save(&self, state: &ReminderState) -> Result<()> {
        state.save(&self.state_path).await
    }

    pub async fn deliver_due(&self) -> Result<usize> {
        let mut state = self.state().await?;
        let now = now_unix();
        let (due, pending): (Vec<_>, Vec<_>) = state
            .reminders
            .into_iter()
            .partition(|reminder| reminder.due_at_unix <= now);
        if due.is_empty() {
            return Ok(0);
        }
        state.reminders = pending;
        self.save(&state).await?;
        let mut delivered = 0;
        for reminder in due {
            let message = format!("⏰ Reminder: {}", reminder.text);
            match self
                .sender
                .send_reminder(self.chat_id, &message, reminder.reply_to_message_id)
                .await
            {
                Ok(()) => delivered += 1,
                Err(error) => {
                    tracing::warn!(%error, reminder_id = %reminder.id, "reminder delivery failed; rescheduling");
                    state.reminders.push(reminder);
                }
            }
        }
        self.save(&state).await?;
        Ok(delivered)
    }
}

impl ReminderState {
    async fn load(path: &Path) -> Result<Self> {
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
            .with_context(|| format!("invalid reminder state at {}", path.display()))
    }

    async fn save(&self, path: &Path) -> Result<()> {
        let encoded = serde_json::to_vec_pretty(self).context("failed to encode reminder state")?;
        let temporary = path.with_extension(format!("{}.tmp", Uuid::now_v7()));
        tokio::fs::write(&temporary, &encoded)
            .await
            .with_context(|| format!("failed to write {}", temporary.display()))?;
        tokio::fs::rename(&temporary, path)
            .await
            .with_context(|| format!("failed to replace {}", path.display()))
    }
}

#[async_trait]
impl<C: ReminderSender> AgentPlugin for ReminderPlugin<C> {
    fn name(&self) -> &str {
        "reminder"
    }

    fn contract(&self) -> String {
        REMINDER_CONTRACT.into()
    }

    fn tools(&self) -> Vec<ToolDef> {
        vec![
            ToolDef {
                name: "reminder.set".into(),
                description: "Schedule a one-shot reminder. Arguments: text (reminder content), in_minutes (positive integer 1-43200). Returns the reminder id and due time.".into(),
                parameters: serde_json::json!({
                    "type": "object",
                    "properties": {
                        "text": { "type": "string" },
                        "in_minutes": { "type": "integer" }
                    },
                    "required": ["text", "in_minutes"]
                }),
            },
            ToolDef {
                name: "reminder.list".into(),
                description: "List scheduled reminders as JSON with id, text, due_at_unix, and reply_to_message_id.".into(),
                parameters: serde_json::json!({ "type": "object", "properties": {} }),
            },
            ToolDef {
                name: "reminder.cancel".into(),
                description: "Cancel a scheduled reminder by id.".into(),
                parameters: serde_json::json!({
                    "type": "object",
                    "properties": { "id": { "type": "string" } },
                    "required": ["id"]
                }),
            },
        ]
    }

    async fn run_tool(&self, _context: &PluginContext, name: &str, args: &Value) -> Result<String> {
        match name {
            "reminder.set" => {
                let text = required_string(args, "text")?;
                let in_minutes = args
                    .get("in_minutes")
                    .and_then(Value::as_u64)
                    .with_context(|| "in_minutes is required")?;
                ensure!(
                    (1..=MAX_REMINDER_MINUTES).contains(&in_minutes),
                    "in_minutes must be between 1 and {MAX_REMINDER_MINUTES}"
                );
                let now = now_unix();
                let reminder = Reminder {
                    id: Uuid::now_v7(),
                    text: text.to_owned(),
                    due_at_unix: now + in_minutes as i64 * 60,
                    reply_to_message_id: *self
                        .reply_to
                        .lock()
                        .expect("reply target lock is not poisoned"),
                    created_at_unix: now,
                };
                let mut state = self.state().await?;
                state.reminders.push(reminder.clone());
                self.save(&state).await?;
                Ok(format!(
                    "Scheduled reminder {} to fire in {in_minutes} minutes (due_at_unix {}).",
                    reminder.id, reminder.due_at_unix
                ))
            }
            "reminder.list" => {
                let state = self.state().await?;
                let mut reminders = state.reminders;
                reminders.sort_by_key(|reminder| reminder.due_at_unix);
                Ok(serde_json::to_string(&reminders)?)
            }
            "reminder.cancel" => {
                let id = required_string(args, "id")?;
                let id = Uuid::parse_str(id).context("id must be a UUID")?;
                let mut state = self.state().await?;
                let before = state.reminders.len();
                state.reminders.retain(|reminder| reminder.id != id);
                if state.reminders.len() == before {
                    bail!("reminder {id} was not found");
                }
                self.save(&state).await?;
                Ok(format!("Canceled reminder {id}"))
            }
            _ => bail!("unknown reminder tool {name}"),
        }
    }
}

pub fn spawn_delivery_loop<C: ReminderSender + 'static>(plugin: std::sync::Arc<ReminderPlugin<C>>) {
    tokio::spawn(async move {
        loop {
            tokio::time::sleep(std::time::Duration::from_secs(5)).await;
            match plugin.deliver_due().await {
                Ok(0) => {}
                Ok(delivered) => tracing::info!(delivered, "delivered due reminders"),
                Err(error) => tracing::warn!(%error, "reminder delivery failed"),
            }
        }
    });
}

fn required_string<'a>(args: &'a Value, name: &str) -> Result<&'a str> {
    args.get(name)
        .and_then(Value::as_str)
        .with_context(|| format!("{name} is required"))
}

fn now_unix() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;

    use super::*;

    struct FakeSender {
        sent: Mutex<Vec<(i64, String, Option<i64>)>>,
    }

    #[async_trait]
    impl ReminderSender for FakeSender {
        async fn send_reminder(
            &self,
            chat_id: i64,
            text: &str,
            reply_to_message_id: Option<i64>,
        ) -> Result<()> {
            self.sent
                .lock()
                .unwrap()
                .push((chat_id, text.to_owned(), reply_to_message_id));
            Ok(())
        }
    }

    fn context() -> PluginContext {
        PluginContext {
            agent_id: Uuid::now_v7(),
            agent_home: PathBuf::from("/tmp/unused"),
        }
    }

    #[tokio::test]
    async fn set_list_and_cancel_reminders_with_persistence() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("reminders.json");
        let sender = FakeSender {
            sent: Mutex::new(Vec::new()),
        };
        let plugin = ReminderPlugin::new(7, sender, path.clone());
        plugin.set_reply_target(Some(11));

        let output = plugin
            .run_tool(
                &context(),
                "reminder.set",
                &serde_json::json!({"text": "take a break", "in_minutes": 30}),
            )
            .await
            .unwrap();
        assert!(output.contains("Scheduled reminder"));

        let listed = plugin
            .run_tool(&context(), "reminder.list", &serde_json::json!({}))
            .await
            .unwrap();
        let parsed: Vec<Reminder> = serde_json::from_str(&listed).unwrap();
        assert_eq!(parsed.len(), 1);
        assert_eq!(parsed[0].text, "take a break");
        assert_eq!(parsed[0].reply_to_message_id, Some(11));

        let id = parsed[0].id.to_string();
        let canceled = plugin
            .run_tool(
                &context(),
                "reminder.cancel",
                &serde_json::json!({"id": id}),
            )
            .await
            .unwrap();
        assert!(canceled.contains("Canceled"));

        let reopened = ReminderPlugin::new(
            7,
            FakeSender {
                sent: Mutex::new(Vec::new()),
            },
            path,
        );
        let listed = reopened
            .run_tool(&context(), "reminder.list", &serde_json::json!({}))
            .await
            .unwrap();
        assert_eq!(
            serde_json::from_str::<Vec<Reminder>>(&listed).unwrap(),
            vec![]
        );
    }

    #[tokio::test]
    async fn reminder_set_validates_the_delay() {
        let directory = tempfile::tempdir().unwrap();
        let plugin = ReminderPlugin::new(
            7,
            FakeSender {
                sent: Mutex::new(Vec::new()),
            },
            directory.path().join("reminders.json"),
        );

        assert!(
            plugin
                .run_tool(
                    &context(),
                    "reminder.set",
                    &serde_json::json!({"text": "now", "in_minutes": 0}),
                )
                .await
                .is_err()
        );
        assert!(
            plugin
                .run_tool(
                    &context(),
                    "reminder.set",
                    &serde_json::json!({"text": "never", "in_minutes": 999_999}),
                )
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn deliver_due_sends_past_reminders_and_keeps_future_ones() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("reminders.json");
        let now = now_unix();
        let state = ReminderState {
            reminders: vec![
                Reminder {
                    id: Uuid::now_v7(),
                    text: "past".into(),
                    due_at_unix: now - 10,
                    reply_to_message_id: Some(3),
                    created_at_unix: now - 60,
                },
                Reminder {
                    id: Uuid::now_v7(),
                    text: "future".into(),
                    due_at_unix: now + 600,
                    reply_to_message_id: None,
                    created_at_unix: now,
                },
            ],
        };
        state.save(&path).await.unwrap();
        let sender = FakeSender {
            sent: Mutex::new(Vec::new()),
        };
        let plugin = ReminderPlugin::new(7, sender, path);

        let delivered = plugin.deliver_due().await.unwrap();

        assert_eq!(delivered, 1);
        let sent = plugin.sender.sent.lock().unwrap().clone();
        assert_eq!(sent[0].0, 7);
        assert_eq!(sent[0].1, "⏰ Reminder: past");
        assert_eq!(sent[0].2, Some(3));
        let remaining = plugin.state().await.unwrap().reminders;
        assert_eq!(remaining.len(), 1);
        assert_eq!(remaining[0].text, "future");
    }
}
