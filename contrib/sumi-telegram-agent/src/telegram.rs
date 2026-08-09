use anyhow::{Context, Result, bail};
use secrecy::{ExposeSecret, SecretString};
use teloxide::{
    Bot,
    net::Download,
    payloads::{
        GetUpdatesSetters, SendDocumentSetters, SendMessageSetters, SendPhotoSetters,
        SetMessageReactionSetters,
    },
    requests::Requester,
    types::{
        AllowedUpdate, ChatAction, ChatId, File, InputFile, MessageId, ParseMode, ReactionType,
        ReplyParameters,
    },
};

const MAX_GET_UPDATES_TIMEOUT_SECONDS: u32 = 50;

#[derive(Clone)]
pub struct TelegramClient {
    bot: Bot,
}

impl TelegramClient {
    pub fn new(token: SecretString) -> Self {
        Self {
            bot: Bot::new(token.expose_secret().to_owned()),
        }
    }

    pub async fn get_updates(&self, offset: Option<i64>) -> Result<Vec<teloxide::types::Update>> {
        let mut request = self.bot.get_updates();
        if let Some(offset) = offset {
            request = request.offset(offset as i32);
        }
        request = request
            .timeout(MAX_GET_UPDATES_TIMEOUT_SECONDS)
            .allowed_updates(vec![AllowedUpdate::Message]);
        request.await.context("Telegram getUpdates failed")
    }

    pub async fn get_file(&self, file_id: &str) -> Result<File> {
        self.bot
            .get_file(file_id.to_owned())
            .await
            .context("Telegram getFile failed")
    }

    pub async fn download_file(&self, file: &File) -> Result<Vec<u8>> {
        let mut bytes = Vec::new();
        self.bot
            .download_file(&file.path, &mut bytes)
            .await
            .context("Telegram file download failed")?;
        Ok(bytes)
    }

    pub async fn send_message(
        &self,
        chat_id: i64,
        text: &str,
        reply_to_message_id: Option<i64>,
        parse_mode: Option<&str>,
    ) -> Result<()> {
        let mut request = self.bot.send_message(ChatId(chat_id), text);
        if let Some(message_id) = reply_to_message_id {
            request = request.reply_parameters(ReplyParameters::new(MessageId(message_id as i32)));
        }
        if let Some(mode) = parse_mode {
            request = request.parse_mode(parse_mode_from(mode)?);
        }
        request.await.context("Telegram sendMessage failed")?;
        Ok(())
    }

    pub async fn send_message_html(
        &self,
        chat_id: i64,
        text: &str,
        reply_to_message_id: Option<i64>,
    ) -> Result<()> {
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
    ) -> Result<()> {
        let input = InputFile::memory(bytes).file_name(file_name.to_owned());
        let mut request = self.bot.send_photo(ChatId(chat_id), input);
        if !caption.is_empty() {
            request = request.caption(caption.to_owned());
        }
        if let Some(message_id) = reply_to_message_id {
            request = request.reply_parameters(ReplyParameters::new(MessageId(message_id as i32)));
        }
        if let Some(mode) = parse_mode {
            request = request.parse_mode(parse_mode_from(mode)?);
        }
        request.await.context("Telegram sendPhoto failed")?;
        Ok(())
    }

    pub async fn send_photo_url(
        &self,
        chat_id: i64,
        url: &str,
        caption: &str,
        reply_to_message_id: Option<i64>,
        parse_mode: Option<&str>,
    ) -> Result<()> {
        let input = InputFile::url(url.parse().context("invalid photo URL")?);
        let mut request = self.bot.send_photo(ChatId(chat_id), input);
        if !caption.is_empty() {
            request = request.caption(caption.to_owned());
        }
        if let Some(message_id) = reply_to_message_id {
            request = request.reply_parameters(ReplyParameters::new(MessageId(message_id as i32)));
        }
        if let Some(mode) = parse_mode {
            request = request.parse_mode(parse_mode_from(mode)?);
        }
        request.await.context("Telegram sendPhoto failed")?;
        Ok(())
    }

    pub async fn send_document(
        &self,
        chat_id: i64,
        file_name: &str,
        bytes: Vec<u8>,
        caption: &str,
        reply_to_message_id: Option<i64>,
        parse_mode: Option<&str>,
    ) -> Result<()> {
        let input = InputFile::memory(bytes).file_name(file_name.to_owned());
        let mut request = self.bot.send_document(ChatId(chat_id), input);
        if !caption.is_empty() {
            request = request.caption(caption.to_owned());
        }
        if let Some(message_id) = reply_to_message_id {
            request = request.reply_parameters(ReplyParameters::new(MessageId(message_id as i32)));
        }
        if let Some(mode) = parse_mode {
            request = request.parse_mode(parse_mode_from(mode)?);
        }
        request.await.context("Telegram sendDocument failed")?;
        Ok(())
    }

    pub async fn set_message_reaction(
        &self,
        chat_id: i64,
        message_id: i64,
        emoji: &str,
    ) -> Result<()> {
        self.bot
            .set_message_reaction(ChatId(chat_id), MessageId(message_id as i32))
            .reaction(vec![ReactionType::Emoji {
                emoji: emoji.to_owned(),
            }])
            .await
            .context("Telegram setMessageReaction failed")?;
        Ok(())
    }

    pub async fn send_chat_action(&self, chat_id: i64, action: &str) -> Result<()> {
        self.bot
            .send_chat_action(ChatId(chat_id), chat_action(action)?)
            .await
            .context("Telegram sendChatAction failed")?;
        Ok(())
    }
}

fn parse_mode_from(mode: &str) -> Result<ParseMode> {
    match mode {
        "HTML" => Ok(ParseMode::Html),
        "Markdown" | "MarkdownV2" => Ok(ParseMode::MarkdownV2),
        _ => bail!("unsupported Telegram parse mode {mode}"),
    }
}

fn chat_action(action: &str) -> Result<ChatAction> {
    match action {
        "typing" => Ok(ChatAction::Typing),
        "upload_photo" => Ok(ChatAction::UploadPhoto),
        "upload_document" => Ok(ChatAction::UploadDocument),
        "record_video" => Ok(ChatAction::RecordVideo),
        "upload_video" => Ok(ChatAction::UploadVideo),
        "record_voice" => Ok(ChatAction::RecordVoice),
        "upload_voice" => Ok(ChatAction::UploadVoice),
        "find_location" => Ok(ChatAction::FindLocation),
        _ => bail!("unsupported Telegram chat action {action}"),
    }
}
