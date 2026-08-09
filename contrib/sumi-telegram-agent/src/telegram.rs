use anyhow::{Context, bail};
use reqwest::{
    StatusCode,
    multipart::{Form, Part},
};
use secrecy::{ExposeSecret, SecretString};
use serde::de::DeserializeOwned;

use crate::types::{
    ApiResponse, File, GetFileParams, GetUpdatesParams, SendMessageParams, SendPhotoUrlParams,
    SetMessageReactionParams, Update,
};

const MAX_GET_UPDATES_TIMEOUT_SECONDS: i64 = 50;

#[derive(Clone)]
pub struct TelegramClient {
    token: SecretString,
    api_base: String,
    http: reqwest::Client,
}

impl TelegramClient {
    pub fn new(token: SecretString) -> Self {
        Self {
            token,
            api_base: "https://api.telegram.org".into(),
            http: reqwest::Client::new(),
        }
    }

    fn api_url(&self, method: &str) -> String {
        format!(
            "{}/bot{}/{}",
            self.api_base,
            self.token.expose_secret(),
            method
        )
    }

    fn file_url(&self, file_path: &str) -> String {
        format!(
            "{}/file/bot{}/{}",
            self.api_base,
            self.token.expose_secret(),
            file_path
        )
    }

    async fn call<T: DeserializeOwned>(
        &self,
        method: &str,
        body: &impl serde::Serialize,
    ) -> anyhow::Result<T> {
        let response = self
            .http
            .post(self.api_url(method))
            .json(body)
            .send()
            .await
            .with_context(|| format!("Telegram {method} request failed"))?;
        let api_response = response
            .json::<ApiResponse<T>>()
            .await
            .context("invalid Telegram API response")?;
        if api_response.ok {
            api_response
                .result
                .ok_or_else(|| anyhow::anyhow!("Telegram {method} returned no result"))
        } else {
            bail!(
                "Telegram {method} failed ({}): {}",
                api_response.error_code.unwrap_or_default(),
                api_response.description.unwrap_or_default()
            )
        }
    }

    pub async fn get_updates(&self, offset: Option<i64>) -> anyhow::Result<Vec<Update>> {
        self.call(
            "getUpdates",
            &GetUpdatesParams {
                offset,
                timeout: MAX_GET_UPDATES_TIMEOUT_SECONDS,
                allowed_updates: vec!["message"],
            },
        )
        .await
    }

    pub async fn get_file(&self, file_id: &str) -> anyhow::Result<File> {
        self.call("getFile", &GetFileParams { file_id }).await
    }

    pub async fn download_file(&self, file_path: &str) -> anyhow::Result<Vec<u8>> {
        let response = self
            .http
            .get(self.file_url(file_path))
            .send()
            .await
            .context("Telegram file download request failed")?;
        if response.status() != StatusCode::OK {
            bail!(
                "Telegram file download failed with HTTP {}",
                response.status()
            );
        }
        Ok(response
            .bytes()
            .await
            .context("Telegram file download failed")?
            .to_vec())
    }

    pub async fn send_message(
        &self,
        chat_id: i64,
        text: &str,
        reply_to_message_id: Option<i64>,
        parse_mode: Option<&str>,
    ) -> anyhow::Result<()> {
        self.call(
            "sendMessage",
            &SendMessageParams {
                chat_id,
                text,
                parse_mode,
                reply_to_message_id,
            },
        )
        .await
        .map(|_: serde_json::Value| ())
    }

    pub async fn send_message_html(
        &self,
        chat_id: i64,
        text: &str,
        reply_to_message_id: Option<i64>,
    ) -> anyhow::Result<()> {
        self.send_message(chat_id, text, reply_to_message_id, Some("HTML"))
            .await
    }

    pub async fn send_photo(
        &self,
        chat_id: i64,
        file_name: &str,
        bytes: Vec<u8>,
        caption: &str,
        reply_to_message_id: Option<i64>,
        parse_mode: Option<&str>,
    ) -> anyhow::Result<()> {
        let mime = guess_mime(file_name);
        let part = Part::bytes(bytes)
            .file_name(file_name.to_owned())
            .mime_str(mime)?;
        let form = send_form_fields(
            Form::new(),
            chat_id,
            caption,
            reply_to_message_id,
            parse_mode,
        )
        .part("photo", part);
        self.send_form("sendPhoto", form).await
    }

    pub async fn send_photo_url(
        &self,
        chat_id: i64,
        url: &str,
        caption: &str,
        reply_to_message_id: Option<i64>,
        parse_mode: Option<&str>,
    ) -> anyhow::Result<()> {
        self.call(
            "sendPhoto",
            &SendPhotoUrlParams {
                chat_id,
                photo: url,
                caption: (!caption.is_empty()).then_some(caption),
                parse_mode,
                reply_to_message_id,
            },
        )
        .await
        .map(|_: serde_json::Value| ())
    }

    pub async fn send_document(
        &self,
        chat_id: i64,
        file_name: &str,
        bytes: Vec<u8>,
        caption: &str,
        reply_to_message_id: Option<i64>,
        parse_mode: Option<&str>,
    ) -> anyhow::Result<()> {
        let mime = guess_mime(file_name);
        let part = Part::bytes(bytes)
            .file_name(file_name.to_owned())
            .mime_str(mime)?;
        let form = send_form_fields(
            Form::new(),
            chat_id,
            caption,
            reply_to_message_id,
            parse_mode,
        )
        .part("document", part);
        self.send_form("sendDocument", form).await
    }

    pub async fn set_message_reaction(
        &self,
        chat_id: i64,
        message_id: i64,
        emoji: &str,
    ) -> anyhow::Result<()> {
        self.call(
            "setMessageReaction",
            &SetMessageReactionParams {
                chat_id,
                message_id,
                reaction: vec![crate::types::Reaction {
                    kind: "emoji",
                    emoji,
                }],
            },
        )
        .await
        .map(|_: serde_json::Value| ())
    }

    pub async fn send_chat_action(&self, chat_id: i64, action: &str) -> anyhow::Result<()> {
        self.call(
            "sendChatAction",
            &serde_json::json!({ "chat_id": chat_id, "action": action }),
        )
        .await
        .map(|_: serde_json::Value| ())
    }

    async fn send_form(&self, method: &str, form: Form) -> anyhow::Result<()> {
        let response = self
            .http
            .post(self.api_url(method))
            .multipart(form)
            .send()
            .await
            .with_context(|| format!("Telegram {method} request failed"))?;
        let api_response = response
            .json::<ApiResponse<serde_json::Value>>()
            .await
            .context("invalid Telegram API response")?;
        if api_response.ok {
            Ok(())
        } else {
            bail!(
                "Telegram {method} failed ({}): {}",
                api_response.error_code.unwrap_or_default(),
                api_response.description.unwrap_or_default()
            )
        }
    }
}

fn send_form_fields(
    form: Form,
    chat_id: i64,
    caption: &str,
    reply_to_message_id: Option<i64>,
    parse_mode: Option<&str>,
) -> Form {
    let form = form.text("chat_id", chat_id.to_string());
    let form = if caption.is_empty() {
        form
    } else {
        form.text("caption", caption.to_owned())
    };
    let form = match reply_to_message_id {
        Some(message_id) => form.text("reply_to_message_id", message_id.to_string()),
        None => form,
    };
    match parse_mode {
        Some(mode) => form.text("parse_mode", mode.to_owned()),
        None => form,
    }
}

pub fn guess_mime(file_name: &str) -> &'static str {
    let lower = file_name.to_ascii_lowercase();
    if lower.ends_with(".jpg") || lower.ends_with(".jpeg") {
        "image/jpeg"
    } else if lower.ends_with(".png") {
        "image/png"
    } else if lower.ends_with(".gif") {
        "image/gif"
    } else if lower.ends_with(".webp") {
        "image/webp"
    } else if lower.ends_with(".pdf") {
        "application/pdf"
    } else if lower.ends_with(".json") {
        "application/json"
    } else if lower.ends_with(".csv") {
        "text/csv"
    } else if lower.ends_with(".txt") || lower.ends_with(".md") {
        "text/plain"
    } else if lower.ends_with(".zip") {
        "application/zip"
    } else {
        "application/octet-stream"
    }
}
