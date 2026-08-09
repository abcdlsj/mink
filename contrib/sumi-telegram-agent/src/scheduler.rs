use std::{
    path::{Path, PathBuf},
    str::FromStr,
    sync::Mutex,
    time::{SystemTime, UNIX_EPOCH},
};

use anyhow::{Context, Result, bail, ensure};
use async_trait::async_trait;
use chrono::{DateTime, Duration, TimeZone};
use chrono_tz::Tz;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sumi_agent_core::{AgentPlugin, PluginContext, ToolDef};
use uuid::Uuid;

const SCHEDULER_CONTRACT: &str = concat!(
    "Scheduler tools: `scheduler.create` schedules an agent task with `prompt` and `next_at` ",
    "(RFC3339 in the chat timezone; the run context includes the current local time as `now`). ",
    "`repeat` is `once`, `daily`, or `weekly` (default `once`). At the scheduled time the agent ",
    "runs a full turn with the prompt as its instruction and delivers the result to this chat, ",
    "then reschedules a recurring task. Use `scheduler.list` to inspect tasks and ",
    "`scheduler.cancel` with the returned id to remove one. Tasks survive restarts.",
);

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Repeat {
    Once,
    Daily,
    Weekly,
}

impl Repeat {
    fn parse(value: Option<&str>) -> Result<Self> {
        match value.unwrap_or("once") {
            "once" => Ok(Self::Once),
            "daily" => Ok(Self::Daily),
            "weekly" => Ok(Self::Weekly),
            other => bail!("repeat must be once, daily, or weekly, got {other}"),
        }
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ScheduledTask {
    pub id: Uuid,
    pub prompt: String,
    pub next_at_unix: i64,
    pub repeat: Repeat,
    #[serde(default)]
    pub reply_to_message_id: Option<i64>,
    pub created_at_unix: i64,
}

impl ScheduledTask {
    pub fn next_occurrence(&self, timezone: Tz) -> Option<i64> {
        let local = timezone.timestamp_opt(self.next_at_unix, 0).single()?;
        let next_local = match self.repeat {
            Repeat::Once => return None,
            Repeat::Daily => local + Duration::days(1),
            Repeat::Weekly => local + Duration::weeks(1),
        };
        Some(next_local.timestamp())
    }

    pub fn local_rfc3339(&self, timezone: Tz) -> String {
        timezone
            .timestamp_opt(self.next_at_unix, 0)
            .single()
            .map(|value| value.to_rfc3339())
            .unwrap_or_else(|| format!("unix {}", self.next_at_unix))
    }
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
struct SchedulerState {
    #[serde(default)]
    tasks: Vec<ScheduledTask>,
}

pub struct SchedulerPlugin {
    state_path: PathBuf,
    timezone: Tz,
    reply_to: Mutex<Option<i64>>,
}

impl SchedulerPlugin {
    pub fn new(state_path: PathBuf, timezone: Tz) -> Self {
        Self {
            state_path,
            timezone,
            reply_to: Mutex::new(None),
        }
    }

    pub fn set_reply_target(&self, message_id: Option<i64>) {
        *self
            .reply_to
            .lock()
            .expect("reply target lock is not poisoned") = message_id;
    }

    pub fn timezone(&self) -> Tz {
        self.timezone
    }

    async fn state(&self) -> Result<SchedulerState> {
        SchedulerState::load(&self.state_path).await
    }

    async fn save(&self, state: &SchedulerState) -> Result<()> {
        state.save(&self.state_path).await
    }

    /// Remove and return tasks whose `next_at_unix` has passed.
    pub async fn take_due(&self, now_unix: i64) -> Result<Vec<ScheduledTask>> {
        let mut state = self.state().await?;
        let (due, pending): (Vec<_>, Vec<_>) = state
            .tasks
            .into_iter()
            .partition(|task| task.next_at_unix <= now_unix);
        if due.is_empty() {
            return Ok(Vec::new());
        }
        state.tasks = pending;
        self.save(&state).await?;
        let mut due = due;
        due.sort_by_key(|task| task.next_at_unix);
        Ok(due)
    }

    /// Re-insert a completed task at its next occurrence, or keep it removed
    /// for one-shot tasks.
    pub async fn reschedule(&self, task: &ScheduledTask) -> Result<Option<ScheduledTask>> {
        let Some(next_at_unix) = task.next_occurrence(self.timezone) else {
            return Ok(None);
        };
        let next = ScheduledTask {
            next_at_unix,
            ..task.clone()
        };
        let mut state = self.state().await?;
        state.tasks.push(next.clone());
        self.save(&state).await?;
        Ok(Some(next))
    }
}

impl SchedulerState {
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
            .with_context(|| format!("invalid scheduler state at {}", path.display()))
    }

    async fn save(&self, path: &Path) -> Result<()> {
        let encoded =
            serde_json::to_vec_pretty(self).context("failed to encode scheduler state")?;
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
impl AgentPlugin for SchedulerPlugin {
    fn name(&self) -> &str {
        "scheduler"
    }

    fn contract(&self) -> String {
        SCHEDULER_CONTRACT.into()
    }

    fn tools(&self) -> Vec<ToolDef> {
        vec![
            ToolDef {
                name: "scheduler.create".into(),
                description: "Schedule an agent task. Arguments: prompt (what the agent must do at the scheduled time), next_at (RFC3339 in the chat timezone), repeat (once|daily|weekly, default once). Returns the task id and next run time.".into(),
                parameters: serde_json::json!({
                    "type": "object",
                    "properties": {
                        "prompt": { "type": "string" },
                        "next_at": { "type": "string" },
                        "repeat": { "type": "string", "enum": ["once", "daily", "weekly"] }
                    },
                    "required": ["prompt", "next_at"]
                }),
            },
            ToolDef {
                name: "scheduler.list".into(),
                description: "List scheduled agent tasks as JSON with id, prompt, next_at_unix, next_at (local RFC3339), and repeat.".into(),
                parameters: serde_json::json!({ "type": "object", "properties": {} }),
            },
            ToolDef {
                name: "scheduler.cancel".into(),
                description: "Cancel a scheduled agent task by id.".into(),
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
            "scheduler.create" => {
                let prompt = required_string(args, "prompt")?;
                let next_at = required_string(args, "next_at")?;
                let repeat = Repeat::parse(args.get("repeat").and_then(Value::as_str))?;
                let parsed =
                    DateTime::parse_from_rfc3339(next_at).context("next_at must be RFC3339")?;
                let next_at_unix = parsed.timestamp();
                ensure!(next_at_unix > now_unix(), "next_at must be in the future");
                let task = ScheduledTask {
                    id: Uuid::now_v7(),
                    prompt: prompt.to_owned(),
                    next_at_unix,
                    repeat,
                    reply_to_message_id: *self
                        .reply_to
                        .lock()
                        .expect("reply target lock is not poisoned"),
                    created_at_unix: now_unix(),
                };
                let mut state = self.state().await?;
                state.tasks.push(task.clone());
                self.save(&state).await?;
                Ok(format!(
                    "Scheduled task {}: next run {} ({repeat:?}).",
                    task.id,
                    task.local_rfc3339(self.timezone)
                ))
            }
            "scheduler.list" => {
                let state = self.state().await?;
                let mut tasks = state.tasks;
                tasks.sort_by_key(|task| task.next_at_unix);
                let view = tasks
                    .iter()
                    .map(|task| {
                        serde_json::json!({
                            "id": task.id,
                            "prompt": task.prompt,
                            "next_at_unix": task.next_at_unix,
                            "next_at": task.local_rfc3339(self.timezone),
                            "repeat": task.repeat,
                        })
                    })
                    .collect::<Vec<_>>();
                Ok(serde_json::to_string(&view)?)
            }
            "scheduler.cancel" => {
                let id = required_string(args, "id")?;
                let id = Uuid::parse_str(id).context("id must be a UUID")?;
                let mut state = self.state().await?;
                let before = state.tasks.len();
                state.tasks.retain(|task| task.id != id);
                if state.tasks.len() == before {
                    bail!("scheduled task {id} was not found");
                }
                self.save(&state).await?;
                Ok(format!("Canceled scheduled task {id}"))
            }
            _ => bail!("unknown scheduler tool {name}"),
        }
    }
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

pub fn parse_timezone(value: &str) -> Result<Tz> {
    Tz::from_str(value).with_context(|| format!("invalid timezone {value}"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Utc;

    fn context() -> PluginContext {
        PluginContext {
            agent_id: Uuid::now_v7(),
            agent_home: PathBuf::from("/tmp/unused"),
        }
    }

    fn timezone() -> Tz {
        Tz::Asia__Shanghai
    }

    #[tokio::test]
    async fn create_list_and_cancel_tasks_with_persistence() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("scheduler.json");
        let plugin = SchedulerPlugin::new(path.clone(), timezone());
        plugin.set_reply_target(Some(11));
        let future = (Utc::now() + Duration::hours(1)).to_rfc3339();

        let output = plugin
            .run_tool(
                &context(),
                "scheduler.create",
                &serde_json::json!({
                    "prompt": "check daily news and summarize",
                    "next_at": future,
                    "repeat": "daily"
                }),
            )
            .await
            .unwrap();
        assert!(output.contains("Scheduled task"));

        let listed = plugin
            .run_tool(&context(), "scheduler.list", &serde_json::json!({}))
            .await
            .unwrap();
        let parsed: Vec<serde_json::Value> = serde_json::from_str(&listed).unwrap();
        assert_eq!(parsed.len(), 1);
        assert_eq!(parsed[0]["prompt"], "check daily news and summarize");
        assert_eq!(parsed[0]["repeat"], "daily");

        let id = parsed[0]["id"].as_str().unwrap();
        let canceled = plugin
            .run_tool(
                &context(),
                "scheduler.cancel",
                &serde_json::json!({"id": id}),
            )
            .await
            .unwrap();
        assert!(canceled.contains("Canceled"));

        let reopened = SchedulerPlugin::new(path, timezone());
        let listed = reopened
            .run_tool(&context(), "scheduler.list", &serde_json::json!({}))
            .await
            .unwrap();
        assert_eq!(
            serde_json::from_str::<Vec<serde_json::Value>>(&listed).unwrap(),
            Vec::<serde_json::Value>::new()
        );
    }

    #[tokio::test]
    async fn create_rejects_past_times_and_bad_repeat() {
        let directory = tempfile::tempdir().unwrap();
        let plugin = SchedulerPlugin::new(directory.path().join("scheduler.json"), timezone());
        let past = (Utc::now() - Duration::minutes(1)).to_rfc3339();

        assert!(
            plugin
                .run_tool(
                    &context(),
                    "scheduler.create",
                    &serde_json::json!({"prompt": "p", "next_at": past}),
                )
                .await
                .is_err()
        );
        assert!(
            plugin
                .run_tool(
                    &context(),
                    "scheduler.create",
                    &serde_json::json!({
                        "prompt": "p",
                        "next_at": (Utc::now() + Duration::hours(1)).to_rfc3339(),
                        "repeat": "monthly"
                    }),
                )
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn take_due_and_reschedule_advance_daily_and_weekly_tasks() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("scheduler.json");
        let now = now_unix();
        let state = SchedulerState {
            tasks: vec![
                ScheduledTask {
                    id: Uuid::now_v7(),
                    prompt: "daily".into(),
                    next_at_unix: now - 10,
                    repeat: Repeat::Daily,
                    reply_to_message_id: Some(3),
                    created_at_unix: now - 60,
                },
                ScheduledTask {
                    id: Uuid::now_v7(),
                    prompt: "weekly".into(),
                    next_at_unix: now - 10,
                    repeat: Repeat::Weekly,
                    reply_to_message_id: None,
                    created_at_unix: now - 60,
                },
                ScheduledTask {
                    id: Uuid::now_v7(),
                    prompt: "once".into(),
                    next_at_unix: now - 10,
                    repeat: Repeat::Once,
                    reply_to_message_id: None,
                    created_at_unix: now - 60,
                },
                ScheduledTask {
                    id: Uuid::now_v7(),
                    prompt: "future".into(),
                    next_at_unix: now + 600,
                    repeat: Repeat::Once,
                    reply_to_message_id: None,
                    created_at_unix: now,
                },
            ],
        };
        state.save(&path).await.unwrap();
        let plugin = SchedulerPlugin::new(path, timezone());

        let due = plugin.take_due(now).await.unwrap();
        assert_eq!(due.len(), 3);

        let once = due.iter().find(|task| task.repeat == Repeat::Once).unwrap();
        assert!(plugin.reschedule(once).await.unwrap().is_none());
        let daily = due
            .iter()
            .find(|task| task.repeat == Repeat::Daily)
            .unwrap();
        let daily = plugin.reschedule(daily).await.unwrap().unwrap();
        assert!(daily.next_at_unix >= now + 86_400 - 120);
        assert_eq!(daily.repeat, Repeat::Daily);
        let weekly = due
            .iter()
            .find(|task| task.repeat == Repeat::Weekly)
            .unwrap();
        let weekly = plugin.reschedule(weekly).await.unwrap().unwrap();
        assert!(weekly.next_at_unix >= now + 7 * 86_400 - 120);
        assert_eq!(weekly.repeat, Repeat::Weekly);

        let remaining = plugin.state().await.unwrap().tasks;
        assert_eq!(remaining.len(), 3);
        assert!(remaining.iter().any(|task| task.prompt == "future"));
        assert!(remaining.iter().any(|task| task.prompt == "daily"));
        assert!(remaining.iter().any(|task| task.prompt == "weekly"));
    }

    #[test]
    fn timezone_parses_env_values() {
        assert_eq!(parse_timezone("Asia/Shanghai").unwrap(), timezone());
        assert!(parse_timezone("Not/AZone").is_err());
    }
}
