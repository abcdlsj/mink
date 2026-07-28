use std::collections::HashMap;

use sqlx::FromRow;
use time::OffsetDateTime;
use uuid::Uuid;

use super::{
    api_error::ApiError,
    attachment::{AttachmentResponse, AttachmentRow},
    message::{MessageResponse, MessageTaskSummary},
};

#[cfg(test)]
tokio::task_local! {
    static HYDRATION_QUERY_COUNT: std::cell::Cell<usize>;
}

#[derive(Default)]
struct HydratedMessage {
    attachments: Vec<AttachmentResponse>,
    mentions: Vec<Uuid>,
    task: Option<MessageTaskSummary>,
}

pub(super) struct MessageHydration {
    messages: HashMap<Uuid, HydratedMessage>,
}

impl MessageHydration {
    pub(super) async fn load(
        database: &sqlx::PgPool,
        message_ids: impl IntoIterator<Item = Uuid>,
    ) -> Result<Self, ApiError> {
        let mut message_ids = message_ids.into_iter().collect::<Vec<_>>();
        message_ids.sort_unstable();
        message_ids.dedup();
        if message_ids.is_empty() {
            return Ok(Self {
                messages: HashMap::new(),
            });
        }

        #[cfg(test)]
        HYDRATION_QUERY_COUNT
            .try_with(|count| count.set(count.get() + 1))
            .ok();
        let rows = sqlx::query_as::<_, HydrationRow>(
            "WITH requested AS ( \
                 SELECT unnest($1::uuid[]) AS message_id \
             ), mentions AS ( \
                 SELECT message_id, array_agg(member_id ORDER BY member_id) AS member_ids \
                 FROM message_mentions WHERE message_id = ANY($1) GROUP BY message_id \
             ) \
             SELECT requested.message_id, \
                    COALESCE(mentions.member_ids, ARRAY[]::uuid[]) AS mentions, \
                    tasks.id AS task_id, tasks.title AS task_title, tasks.status AS task_status, \
                    tasks.assigned_agent_member_id, assignees.display_name AS assignee_name, \
                    attachments.id AS attachment_id, attachments.space_id AS attachment_space_id, \
                    attachments.uploader_member_id AS attachment_uploader_member_id, \
                    attachments.original_name AS attachment_original_name, \
                    attachments.media_type AS attachment_media_type, \
                    attachments.size AS attachment_size, attachments.sha256 AS attachment_sha256, \
                    attachments.status AS attachment_status, \
                    attachments.created_at AS attachment_created_at \
             FROM requested \
             LEFT JOIN mentions ON mentions.message_id = requested.message_id \
             LEFT JOIN tasks ON tasks.source_message_id = requested.message_id \
             LEFT JOIN members assignees ON assignees.id = tasks.assigned_agent_member_id \
             LEFT JOIN message_attachments \
                    ON message_attachments.message_id = requested.message_id \
             LEFT JOIN attachments ON attachments.id = message_attachments.attachment_id \
             ORDER BY requested.message_id, message_attachments.position",
        )
        .bind(&message_ids)
        .fetch_all(database)
        .await
        .map_err(ApiError::database)?;

        let mut messages = message_ids
            .into_iter()
            .map(|message_id| (message_id, HydratedMessage::default()))
            .collect::<HashMap<_, _>>();
        for row in rows {
            let hydrated = messages
                .get_mut(&row.message_id)
                .ok_or(ApiError::Internal)?;
            if hydrated.task.is_none() {
                hydrated.task = row.task_summary()?;
            }
            if let Some(attachment) = row.attachment()? {
                hydrated.attachments.push(attachment);
            }
            hydrated.mentions = row.mentions;
        }
        Ok(Self { messages })
    }

    pub(super) fn apply_to_responses(&self, messages: &mut [MessageResponse]) {
        for message in messages {
            if let Some(hydrated) = self.messages.get(&message.id) {
                message.attachments.clone_from(&hydrated.attachments);
                message.mentions.clone_from(&hydrated.mentions);
                message.task.clone_from(&hydrated.task);
            }
        }
    }

    pub(super) fn apply_to_agent_messages(
        &self,
        messages: &mut [serde_json::Value],
    ) -> Result<(), ApiError> {
        for message in messages {
            let message_id = message
                .get("id")
                .and_then(serde_json::Value::as_str)
                .and_then(|value| Uuid::parse_str(value).ok())
                .ok_or(ApiError::Internal)?;
            let hydrated = self.messages.get(&message_id).ok_or(ApiError::Internal)?;
            let object = message.as_object_mut().ok_or(ApiError::Internal)?;
            object.insert(
                "attachments".to_owned(),
                serde_json::to_value(&hydrated.attachments).map_err(|_| ApiError::Internal)?,
            );
            object.insert(
                "mentions".to_owned(),
                serde_json::to_value(&hydrated.mentions).map_err(|_| ApiError::Internal)?,
            );
        }
        Ok(())
    }
}

#[cfg(test)]
pub(super) async fn observe_query_count<F, T>(future: F) -> (T, usize)
where
    F: std::future::Future<Output = T>,
{
    HYDRATION_QUERY_COUNT
        .scope(std::cell::Cell::new(0), async move {
            let output = future.await;
            let count = HYDRATION_QUERY_COUNT.with(std::cell::Cell::get);
            (output, count)
        })
        .await
}

#[derive(FromRow)]
struct HydrationRow {
    message_id: Uuid,
    mentions: Vec<Uuid>,
    task_id: Option<Uuid>,
    task_title: Option<String>,
    task_status: Option<String>,
    assigned_agent_member_id: Option<Uuid>,
    assignee_name: Option<String>,
    attachment_id: Option<Uuid>,
    attachment_space_id: Option<Uuid>,
    attachment_uploader_member_id: Option<Uuid>,
    attachment_original_name: Option<String>,
    attachment_media_type: Option<String>,
    attachment_size: Option<i64>,
    attachment_sha256: Option<Vec<u8>>,
    attachment_status: Option<String>,
    attachment_created_at: Option<OffsetDateTime>,
}

impl HydrationRow {
    fn task_summary(&self) -> Result<Option<MessageTaskSummary>, ApiError> {
        self.task_id
            .map(|id| {
                Ok(MessageTaskSummary {
                    id,
                    title: self.task_title.clone().ok_or(ApiError::Internal)?,
                    status: self.task_status.clone().ok_or(ApiError::Internal)?,
                    assigned_agent_member_id: self.assigned_agent_member_id,
                    assignee_name: self.assignee_name.clone(),
                })
            })
            .transpose()
    }

    fn attachment(&self) -> Result<Option<AttachmentResponse>, ApiError> {
        self.attachment_id
            .map(|id| {
                Ok(AttachmentRow {
                    id,
                    space_id: self.attachment_space_id.ok_or(ApiError::Internal)?,
                    uploader_member_id: self
                        .attachment_uploader_member_id
                        .ok_or(ApiError::Internal)?,
                    original_name: self
                        .attachment_original_name
                        .clone()
                        .ok_or(ApiError::Internal)?,
                    media_type: self
                        .attachment_media_type
                        .clone()
                        .ok_or(ApiError::Internal)?,
                    size: self.attachment_size,
                    sha256: self.attachment_sha256.clone(),
                    status: self.attachment_status.clone().ok_or(ApiError::Internal)?,
                    created_at: self.attachment_created_at.ok_or(ApiError::Internal)?,
                }
                .into())
            })
            .transpose()
    }
}
