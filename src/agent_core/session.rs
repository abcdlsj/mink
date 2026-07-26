use crate::agent_core::types::Message;

#[derive(Clone, Debug, Default)]
pub struct Session {
    pub messages: Vec<Message>,
}

impl Session {
    pub fn add(&mut self, message: Message) {
        self.messages.push(message);
    }

    /// Strips image attachments from all user messages.
    /// Returns true if any images were removed.
    pub fn strip_image_attachments(&mut self, add_note: bool) -> bool {
        let mut changed = false;
        for m in &mut self.messages {
            if m.role != "user" || m.attachments.is_empty() {
                continue;
            }
            let before = m.attachments.len();
            m.attachments
                .retain(|a| a.kind != "image" || (a.url.is_empty() && a.data.is_empty()));
            if m.attachments.len() < before {
                changed = true;
                if m.attachments.is_empty() {
                    m.attachments = Vec::new();
                }
                if add_note && !m.content.contains(IMAGE_UNSUPPORTED_NOTE) {
                    if !m.content.is_empty() {
                        m.content.push_str("\n\n");
                    }
                    m.content.push_str(IMAGE_UNSUPPORTED_NOTE);
                }
            }
        }
        changed
    }
}

const IMAGE_UNSUPPORTED_NOTE: &str = "[System note: Image payloads were omitted because the current model endpoint does not support image input. Do not infer visual details; tell the user you cannot inspect the image and ask for a text description or a vision-capable model.]";
