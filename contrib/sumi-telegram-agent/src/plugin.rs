use anyhow::{Context, Result, bail, ensure};
use async_trait::async_trait;
use serde_json::Value;
use sumi_builtin_agent::{AgentPlugin, PluginContext, ToolDef, agent_rooted_path};

use crate::{telegram::TelegramClient, text::sanitize_file_name};

pub const DEFAULT_MAX_SEND_BYTES: usize = 20 * 1024 * 1024;

#[async_trait]
pub trait FileSender: Send + Sync {
    async fn send_photo(
        &self,
        chat_id: i64,
        file_name: &str,
        bytes: Vec<u8>,
        caption: &str,
    ) -> Result<()>;

    async fn send_document(
        &self,
        chat_id: i64,
        file_name: &str,
        bytes: Vec<u8>,
        caption: &str,
    ) -> Result<()>;
}

#[async_trait]
impl FileSender for TelegramClient {
    async fn send_photo(
        &self,
        chat_id: i64,
        file_name: &str,
        bytes: Vec<u8>,
        caption: &str,
    ) -> Result<()> {
        self.send_photo(chat_id, file_name, bytes, caption).await
    }

    async fn send_document(
        &self,
        chat_id: i64,
        file_name: &str,
        bytes: Vec<u8>,
        caption: &str,
    ) -> Result<()> {
        self.send_document(chat_id, file_name, bytes, caption).await
    }
}

pub struct TelegramPlugin<C: FileSender> {
    chat_id: i64,
    sender: C,
    max_send_bytes: usize,
}

impl<C: FileSender> TelegramPlugin<C> {
    pub fn new(chat_id: i64, sender: C) -> Self {
        Self {
            chat_id,
            sender,
            max_send_bytes: DEFAULT_MAX_SEND_BYTES,
        }
    }

    #[allow(dead_code)]
    pub fn with_max_send_bytes(mut self, max_send_bytes: usize) -> Self {
        self.max_send_bytes = max_send_bytes;
        self
    }
}

#[async_trait]
impl<C: FileSender> AgentPlugin for TelegramPlugin<C> {
    fn name(&self) -> &str {
        "telegram"
    }

    fn contract(&self) -> String {
        concat!(
            "Telegram channel: your final reply text is delivered automatically to the current ",
            "chat, so do not summarize that you are replying. Use telegram.send_file or ",
            "telegram.send_image with a workspace/... or memory/... path to deliver a file to the ",
            "user. Paths are relative to the Agent Home root. Sent files are returned to the user ",
            "as a Telegram document or photo with an optional short caption.",
        )
        .into()
    }

    fn tools(&self) -> Vec<ToolDef> {
        vec![
            ToolDef {
                name: "telegram.send_file".into(),
                description: "Send a file (any type) from workspace/ or memory/ to the current Telegram chat. Arguments: path (e.g. workspace/report.pdf), caption (optional short text).".into(),
                parameters: serde_json::json!({
                    "type": "object",
                    "properties": {
                        "path": { "type": "string" },
                        "caption": { "type": "string" }
                    },
                    "required": ["path"]
                }),
            },
            ToolDef {
                name: "telegram.send_image".into(),
                description: "Send an image from workspace/ or memory/ to the current Telegram chat as a photo. Arguments: path (e.g. workspace/diagram.png), caption (optional short text).".into(),
                parameters: serde_json::json!({
                    "type": "object",
                    "properties": {
                        "path": { "type": "string" },
                        "caption": { "type": "string" }
                    },
                    "required": ["path"]
                }),
            },
        ]
    }

    async fn run_tool(&self, context: &PluginContext, name: &str, args: &Value) -> Result<String> {
        let path = required_string(args, "path")?;
        let caption = args.get("caption").and_then(Value::as_str).unwrap_or("");
        let (root, relative) = agent_rooted_path(&context.agent_home, path)?;
        let file_path = root.join(&relative);
        let bytes = tokio::fs::read(&file_path)
            .await
            .with_context(|| format!("failed to read {path}"))?;
        ensure!(
            bytes.len() <= self.max_send_bytes,
            "file exceeds the Telegram send limit of {} MiB",
            self.max_send_bytes / (1024 * 1024)
        );
        let file_name = sanitize_file_name(
            relative
                .file_name()
                .and_then(|name| name.to_str())
                .unwrap_or("attachment"),
        );
        match name {
            "telegram.send_image" => {
                self.sender
                    .send_photo(self.chat_id, &file_name, bytes, caption)
                    .await?;
            }
            "telegram.send_file" => {
                self.sender
                    .send_document(self.chat_id, &file_name, bytes, caption)
                    .await?;
            }
            _ => bail!("unknown telegram tool {name}"),
        }
        Ok(format!("Sent {path} to the Telegram chat"))
    }
}

fn required_string<'a>(args: &'a Value, name: &str) -> Result<&'a str> {
    args.get(name)
        .and_then(Value::as_str)
        .with_context(|| format!("{name} is required"))
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;

    use super::*;

    type SentFile = (i64, String, Vec<u8>, String);

    struct FakeSender {
        sent: Mutex<Vec<SentFile>>,
    }

    #[async_trait]
    impl FileSender for FakeSender {
        async fn send_photo(
            &self,
            chat_id: i64,
            file_name: &str,
            bytes: Vec<u8>,
            caption: &str,
        ) -> Result<()> {
            self.sent.lock().unwrap().push((
                chat_id,
                file_name.to_owned(),
                bytes,
                caption.to_owned(),
            ));
            Ok(())
        }

        async fn send_document(
            &self,
            chat_id: i64,
            file_name: &str,
            bytes: Vec<u8>,
            caption: &str,
        ) -> Result<()> {
            self.sent.lock().unwrap().push((
                chat_id,
                file_name.to_owned(),
                bytes,
                caption.to_owned(),
            ));
            Ok(())
        }
    }

    #[tokio::test]
    async fn plugin_sends_a_file_from_workspace_with_a_caption() {
        let home = tempfile::tempdir().unwrap();
        let workspace = home.path().join("workspace");
        tokio::fs::create_dir_all(&workspace).await.unwrap();
        tokio::fs::write(workspace.join("report: final.pdf"), b"pdf-bytes")
            .await
            .unwrap();
        let sender = FakeSender {
            sent: Mutex::new(Vec::new()),
        };
        let plugin = TelegramPlugin::new(7, sender);

        let output = plugin
            .run_tool(
                &PluginContext {
                    agent_id: uuid::Uuid::now_v7(),
                    agent_home: home.path().to_owned(),
                },
                "telegram.send_file",
                &serde_json::json!({"path": "workspace/report: final.pdf", "caption": "the report"}),
            )
            .await
            .unwrap();

        assert!(output.contains("report: final.pdf"));
        let sent = plugin.sender.sent.lock().unwrap();
        assert_eq!(sent.len(), 1);
        assert_eq!(sent[0].0, 7);
        assert_eq!(sent[0].1, "report_ final.pdf");
        assert_eq!(sent[0].2, b"pdf-bytes");
        assert_eq!(sent[0].3, "the report");
    }

    #[tokio::test]
    async fn plugin_rejects_files_over_the_send_limit_and_escape_paths() {
        let home = tempfile::tempdir().unwrap();
        let workspace = home.path().join("workspace");
        tokio::fs::create_dir_all(&workspace).await.unwrap();
        tokio::fs::write(workspace.join("large.bin"), b"12345")
            .await
            .unwrap();
        let sender = FakeSender {
            sent: Mutex::new(Vec::new()),
        };
        let plugin = TelegramPlugin::new(7, sender).with_max_send_bytes(4);

        assert!(
            plugin
                .run_tool(
                    &PluginContext {
                        agent_id: uuid::Uuid::now_v7(),
                        agent_home: home.path().to_owned(),
                    },
                    "telegram.send_file",
                    &serde_json::json!({"path": "workspace/large.bin"}),
                )
                .await
                .is_err()
        );
        assert!(
            plugin
                .run_tool(
                    &PluginContext {
                        agent_id: uuid::Uuid::now_v7(),
                        agent_home: home.path().to_owned(),
                    },
                    "telegram.send_file",
                    &serde_json::json!({"path": "../secret.bin"}),
                )
                .await
                .is_err()
        );
    }
}
