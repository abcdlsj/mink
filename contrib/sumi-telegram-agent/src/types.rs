//! Telegram message helpers. The Bot API models come from `teloxide`.

pub use teloxide::types::Message;

/// Combined text of a message: `text` for plain messages, `caption` for media.
pub fn text_content(message: &Message) -> String {
    message
        .text()
        .map(str::to_owned)
        .or_else(|| message.caption().map(str::to_owned))
        .unwrap_or_default()
}
