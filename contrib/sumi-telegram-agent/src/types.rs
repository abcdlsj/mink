use serde::{Deserialize, Serialize, de::DeserializeOwned};

#[derive(Clone, Debug, Deserialize)]
pub struct Update {
    pub update_id: i64,
    #[serde(default)]
    pub message: Option<Message>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Message {
    pub message_id: i64,
    pub chat: Chat,
    #[serde(default)]
    pub from: Option<User>,
    #[serde(default)]
    pub text: Option<String>,
    #[serde(default)]
    pub caption: Option<String>,
    #[serde(default)]
    pub photo: Vec<PhotoSize>,
    #[serde(default)]
    pub document: Option<Document>,
    pub date: i64,
}

impl Message {
    pub fn text_content(&self) -> String {
        self.text
            .clone()
            .or_else(|| self.caption.clone())
            .unwrap_or_default()
    }
}

#[derive(Clone, Debug, Deserialize)]
pub struct Chat {
    pub id: i64,
    #[serde(default)]
    pub first_name: Option<String>,
    #[serde(default)]
    pub username: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct User {
    pub id: i64,
    #[serde(default)]
    pub first_name: Option<String>,
    #[serde(default)]
    pub username: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct PhotoSize {
    pub file_id: String,
    #[serde(default)]
    pub file_unique_id: String,
    #[allow(dead_code)]
    #[serde(default)]
    pub width: i64,
    #[allow(dead_code)]
    #[serde(default)]
    pub height: i64,
    #[serde(default)]
    pub file_size: Option<i64>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Document {
    pub file_id: String,
    #[serde(default)]
    pub file_unique_id: String,
    #[serde(default)]
    pub file_name: Option<String>,
    #[serde(default)]
    pub mime_type: Option<String>,
    #[serde(default)]
    pub file_size: Option<i64>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct File {
    #[allow(dead_code)]
    pub file_id: String,
    #[serde(default)]
    pub file_path: Option<String>,
    #[serde(default)]
    pub file_size: Option<i64>,
}

#[derive(Deserialize)]
#[serde(bound(deserialize = "T: DeserializeOwned"))]
pub struct ApiResponse<T> {
    pub ok: bool,
    #[serde(default)]
    pub result: Option<T>,
    #[serde(default)]
    pub description: Option<String>,
    #[serde(default)]
    pub error_code: Option<i64>,
}

#[derive(Serialize)]
pub struct GetUpdatesParams {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub offset: Option<i64>,
    pub timeout: i64,
    pub allowed_updates: Vec<&'static str>,
}

#[derive(Serialize)]
pub struct SendMessageParams<'a> {
    pub chat_id: i64,
    pub text: &'a str,
}

#[derive(Serialize)]
pub struct GetFileParams<'a> {
    pub file_id: &'a str,
}
