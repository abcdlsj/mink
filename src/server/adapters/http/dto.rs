use super::*;
use crate::server::adapters::postgres::{
    ActionAgentRow, ActionChannelRow, AgentRow, AttachmentRow, ChannelRow, MemberRow, MessageRow,
    MessageTaskRow, PostgresQueries, RunRow, SpaceRow, TaskRefRow, TaskRow, ThreadReferenceRow,
};

pub(super) const SPACE_ACCENTS: [&str; 4] = ["#FE7DA8", "#27CCF3", "#FFD440", "#A9D877"];

pub(super) fn normalize_space_accent(input: &str) -> Option<String> {
    let value = input.trim().to_uppercase();
    SPACE_ACCENTS
        .iter()
        .find(|candidate| **candidate == value)
        .map(|candidate| (*candidate).to_owned())
}

#[derive(Deserialize)]
pub(super) struct RegisterBody {
    pub(super) display_name: String,
    pub(super) email: String,
    pub(super) password: String,
}

#[derive(Deserialize)]
pub(super) struct LoginBody {
    pub(super) email: String,
    pub(super) password: String,
}

#[derive(Deserialize)]
pub(super) struct CreateSpaceBody {
    pub(super) name: String,
    pub(super) slug: String,
    pub(super) accent: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CreateChannelBody {
    pub(super) slug: String,
    pub(super) kind: String,
    pub(super) topic: Option<String>,
    #[serde(default)]
    pub(super) agent_member_ids: Vec<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct AddChannelAgentsBody {
    pub(super) agent_member_ids: Vec<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CreateAgentBody {
    pub(super) computer_id: Uuid,
    pub(super) name: String,
    pub(super) role_text: String,
    pub(super) driver_kind: String,
    pub(super) access_level: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CreateMessageBody {
    pub(super) body_markdown: String,
    #[serde(default)]
    pub(super) mentions: Vec<Uuid>,
    #[serde(default)]
    pub(super) mention_all: bool,
    #[serde(default)]
    pub(super) attachment_ids: Vec<Uuid>,
    pub(super) reply_to_message_id: Option<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct UpdateMessageBody {
    pub(super) body_markdown: String,
    #[serde(default)]
    pub(super) mentions: Vec<Uuid>,
    #[serde(default)]
    pub(super) mention_all: bool,
}

#[derive(Deserialize)]
pub(super) struct BeginPairingBody {
    pub(super) token_hash: String,
    pub(super) hostname: String,
    pub(super) os: String,
    pub(super) daemon_version: String,
}

#[derive(Deserialize)]
pub(super) struct PairingCodeQuery {
    pub(super) code: String,
}

#[derive(Deserialize)]
pub(super) struct ConfirmPairingBody {
    pub(super) code: String,
    pub(super) name: String,
    pub(super) space_id: Uuid,
}

#[derive(Deserialize)]
pub(super) struct InviteHumanBody {
    pub(super) email: String,
}

#[derive(Deserialize)]
pub(super) struct OpenDirectMessageBody {
    pub(super) member_id: Uuid,
}

#[derive(Deserialize)]
pub(super) struct UpdateAgentBody {
    pub(super) role_text: Option<String>,
    pub(super) lifecycle: Option<LifecycleActionBody>,
}

#[derive(Deserialize)]
pub(super) struct LifecycleActionBody {
    pub(super) action: String,
    pub(super) mode: Option<String>,
}

#[derive(Deserialize)]
pub(super) struct UpdateMemberBody {
    pub(super) access_level: Option<String>,
}

#[derive(Deserialize)]
pub(super) struct AgentActionRequest {
    pub(super) context: capability::RunContext,
    pub(super) action: capability::Action,
    pub(super) idempotency_key: Option<IdempotencyKey>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct ReadMemoryBody {
    pub(super) path: String,
}

pub(super) fn space_response(
    id: Uuid,
    name: &str,
    slug: &str,
    accent: &str,
    owner: Uuid,
    current: Uuid,
    general: Uuid,
) -> SpaceResponse {
    SpaceResponse {
        id,
        name: name.to_owned(),
        slug: slug.to_owned(),
        accent: accent.to_owned(),
        owner_member_id: owner,
        current_member_id: current,
        general_channel_id: general,
    }
}

pub(super) fn space_row(row: &SpaceRow) -> SpaceResponse {
    space_response(
        row.id,
        &row.name,
        &row.slug,
        &row.accent,
        row.owner_member_id,
        row.current_member_id,
        row.general_channel_id,
    )
}

pub(super) fn channel_row(row: &ChannelRow, creator: Uuid) -> Result<ChannelResponse, ApiError> {
    let kind = match row.kind.as_str() {
        "public" => ChannelKindCode::Public,
        "private" => ChannelKindCode::Private,
        _ => return Err(ApiError::internal()),
    };
    let slug = row.slug.clone().ok_or_else(ApiError::internal)?;
    Ok(ChannelResponse {
        id: row.id,
        space_id: row.space_id,
        slug,
        topic: row.topic.clone(),
        kind,
        created_by_member_id: creator,
        joined: row.joined,
        archived_at: optional_timestamp(row.archived_at),
    })
}

pub(super) async fn member_row(
    queries: &PostgresQueries,
    row: &MemberRow,
) -> Result<MemberResponse, ApiError> {
    let id = row.id;
    let permissions = queries
        .member_permissions(id)
        .await
        .map_err(application_error)?;
    Ok(MemberResponse {
        id,
        kind: match row.kind.as_str() {
            "human" => MemberKindCode::Human,
            "agent" => MemberKindCode::Agent,
            _ => return Err(ApiError::internal()),
        },
        display_name: row.display_name.clone(),
        access_level: match row.access_level.as_str() {
            "owner" => AccessLevelCode::Owner,
            "admin" => AccessLevelCode::Admin,
            "member" => AccessLevelCode::Member,
            _ => return Err(ApiError::internal()),
        },
        permissions,
    })
}

fn task_status_code(value: &str) -> Result<TaskStatus, ApiError> {
    Ok(match value {
        "todo" => TaskStatus::Todo,
        "in_progress" => TaskStatus::InProgress,
        "in_review" => TaskStatus::InReview,
        "done" => TaskStatus::Done,
        "closed" => TaskStatus::Closed,
        _ => return Err(ApiError::internal()),
    })
}

/// The one unfinished or finished Task bound to a Thread, preferring the Source Thread.
pub(super) async fn message_task_summary(
    queries: &PostgresQueries,
    thread_id: Uuid,
) -> Result<Option<MessageTaskSummary>, ApiError> {
    let row = queries
        .task_for_thread(thread_id)
        .await
        .map_err(application_error)?;
    let Some(row) = row else { return Ok(None) };
    let active_focus = row.active_focus_thread_id;
    Ok(Some(MessageTaskSummary {
        id: row.id,
        seq: u64::try_from(row.seq).map_err(|_| ApiError::internal())?,
        title: row.title,
        status: task_status_code(&row.status)?,
        assignee_agent_member_id: row.assignee_agent_member_id,
        assignee_name: row.assignee_name,
        working_elsewhere: active_focus.is_some_and(|focus| focus != thread_id),
    }))
}

/// Extracts `!<seq>` tokens at word boundaries from Message Markdown.
fn parse_task_ref_seqs(body: &str) -> Vec<i64> {
    let bytes = body.as_bytes();
    let mut seqs = Vec::new();
    let mut index = 0;
    while index < bytes.len() {
        let starts_token = bytes[index] == b'!'
            && (index == 0
                || !bytes[index - 1].is_ascii_alphanumeric() && bytes[index - 1] != b'_');
        if starts_token {
            let mut end = index + 1;
            while end < bytes.len() && bytes[end].is_ascii_digit() {
                end += 1;
            }
            let boundary_ok =
                end == bytes.len() || !bytes[end].is_ascii_alphanumeric() && bytes[end] != b'_';
            if end > index + 1
                && boundary_ok
                && let Some(seq) = std::str::from_utf8(&bytes[index + 1..end])
                    .ok()
                    .and_then(|digits| digits.parse::<i64>().ok())
            {
                seqs.push(seq);
            }
            index = end;
        } else {
            index += 1;
        }
    }
    seqs
}

/// Resolves `!<seq>` tokens in a Message to Tasks of the same Space.
pub(super) async fn task_refs_for_message(
    queries: &PostgresQueries,
    space_id: Uuid,
    body: Option<&str>,
) -> Result<Vec<MessageTaskRefResponse>, ApiError> {
    let Some(body) = body else {
        return Ok(Vec::new());
    };
    let seqs = parse_task_ref_seqs(body);
    if seqs.is_empty() {
        return Ok(Vec::new());
    }
    let rows = queries
        .task_refs(space_id, &seqs)
        .await
        .map_err(application_error)?;
    let mut refs = rows
        .iter()
        .map(|row| {
            Ok(MessageTaskRefResponse {
                seq: u64::try_from(row.seq).map_err(|_| ApiError::internal())?,
                task_id: row.id,
                title: row.title.clone(),
                status: task_status_code(&row.status)?,
            })
        })
        .collect::<Result<Vec<_>, ApiError>>()?;
    refs.sort_by_key(|task_ref| task_ref.seq);
    Ok(refs)
}

pub(super) async fn message_row(
    queries: &PostgresQueries,
    row: &MessageRow,
) -> Result<MessageResponse, ApiError> {
    let author = queries
        .message_author(row.author_member_id)
        .await
        .map_err(application_error)?;
    let replies = queries
        .reply_count(row.thread_id)
        .await
        .map_err(application_error)?;
    let attachments = queries
        .attachments_for_message(row.id)
        .await
        .map_err(application_error)?;
    let mut attachment_views = Vec::with_capacity(attachments.len());
    for attachment in &attachments {
        attachment_views.push(attachment_row(attachment)?);
    }
    let attention_failures = queries
        .attention_failures(row.id)
        .await
        .map_err(application_error)?
        .iter()
        .map(|failure| AttentionFailureResponse {
            agent_member_id: failure.member_id,
            error_code: failure.last_error_code.clone(),
            retrying: true,
        })
        .collect::<Vec<_>>();
    let mentions = queries
        .message_mentions(row.id)
        .await
        .map_err(application_error)?;
    let task = if row.placement == "root" {
        message_task_summary(queries, row.thread_id).await?
    } else {
        None
    };
    let task_refs =
        task_refs_for_message(queries, row.space_id, row.body_markdown.as_deref()).await?;
    let content = match row.content_kind.as_str() {
        "text" => MessageContentResponse::Text {
            body_markdown: row.body_markdown.clone().unwrap_or_default(),
        },
        "channel_created" => {
            let target_id = row.action_channel_id.ok_or_else(ApiError::internal)?;
            let target = queries
                .channel_action(target_id)
                .await
                .map_err(application_error)?;
            MessageContentResponse::ChannelCreated {
                channel: ActionChannelResponse {
                    id: target.id,
                    slug: target.slug.unwrap_or_default(),
                    available: target.archived_at.is_none(),
                },
            }
        }
        "agent_created" => {
            let target_id = row.action_agent_member_id.ok_or_else(ApiError::internal)?;
            let target = queries
                .agent_action(target_id)
                .await
                .map_err(application_error)?;
            MessageContentResponse::AgentCreated {
                agent: ActionAgentResponse {
                    member_id: target.member_id,
                    name: target.display_name,
                    lifecycle: match target.lifecycle.as_str() {
                        "suspended" => AgentLifecycle::Suspended,
                        "retired" => AgentLifecycle::Retired,
                        _ => AgentLifecycle::Active,
                    },
                    available: target.retired_at.is_none(),
                },
            }
        }
        "system_notice" => MessageContentResponse::SystemNotice {
            body_markdown: row.body_markdown.clone().unwrap_or_default(),
        },
        _ => return Err(ApiError::internal()),
    };
    Ok(MessageResponse {
        id: row.id,
        channel_id: row.channel_id,
        thread_id: row.thread_id,
        seq: u64::try_from(row.channel_seq).map_err(|_| ApiError::internal())?,
        placement: match row.placement.as_str() {
            "root" => MessagePlacement::Root,
            "reply" => MessagePlacement::Reply,
            _ => return Err(ApiError::internal()),
        },
        author: MessageAuthor {
            id: author.id,
            kind: match author.kind.as_str() {
                "human" => MemberKindCode::Human,
                "agent" => MemberKindCode::Agent,
                _ => return Err(ApiError::internal()),
            },
            display_name: author.display_name,
        },
        content,
        mentions,
        mention_all: row.mention_all,
        attachments: attachment_views,
        reply_count: u64::try_from(replies).map_err(|_| ApiError::internal())?,
        task,
        task_refs,
        attention_failures,
        created_at: timestamp(row.created_at),
        edited_at: optional_timestamp(row.edited_at),
        deleted_at: optional_timestamp(row.deleted_at),
    })
}

pub(super) fn attachment_row(row: &AttachmentRow) -> Result<AttachmentResponse, ApiError> {
    Ok(AttachmentResponse {
        id: row.id,
        space_id: row.space_id,
        uploader_member_id: row.uploader_member_id,
        original_name: row.name.clone(),
        media_type: row.media_type.clone(),
        size: row
            .length
            .map(u64::try_from)
            .transpose()
            .map_err(|_| ApiError::internal())?,
        sha256: row.sha256.clone().map(hex::encode),
        status: match row.status.as_str() {
            "uploading" => AttachmentStatus::Uploading,
            "ready" => AttachmentStatus::Ready,
            "deleted" => AttachmentStatus::Deleted,
            _ => return Err(ApiError::internal()),
        },
        upload_path: None,
        download_path: None,
        created_at: timestamp(row.created_at),
    })
}

pub(super) async fn task_projection(
    queries: &PostgresQueries,
    task_id: Uuid,
) -> Result<TaskResponse, ApiError> {
    let row = queries
        .task(task_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    let source = thread_reference(queries, row.source_thread_id, ThreadRelation::Source).await?;
    let related_ids = queries
        .task_related_threads(task_id)
        .await
        .map_err(application_error)?;
    let mut related = Vec::with_capacity(related_ids.len());
    for id in related_ids {
        related.push(thread_reference(queries, id, ThreadRelation::Related).await?);
    }
    let result_message = if let Some(message_id) = row.result_message_id {
        let message = queries
            .message(message_id)
            .await
            .map_err(application_error)?
            .ok_or_else(ApiError::not_found)?;
        Some(message_row(queries, &message).await?)
    } else {
        None
    };
    let run_ids = queries
        .task_run_ids(task_id)
        .await
        .map_err(application_error)?;
    let mut runs = Vec::with_capacity(run_ids.len());
    for run_id in run_ids {
        runs.push(run_projection(queries, run_id).await?);
    }
    let current_run = runs.iter().find(|run| run.outcome.is_none()).cloned();
    Ok(TaskResponse {
        id: task_id,
        seq: u64::try_from(row.seq).map_err(|_| ApiError::internal())?,
        space_id: row.space_id,
        title: row.title,
        status: match row.status.as_str() {
            "todo" => TaskStatus::Todo,
            "in_progress" => TaskStatus::InProgress,
            "in_review" => TaskStatus::InReview,
            "done" => TaskStatus::Done,
            "closed" => TaskStatus::Closed,
            _ => return Err(ApiError::internal()),
        },
        creator_member_id: row.creator_member_id,
        creator_name: row.creator_name,
        assignee_agent_member_id: row.assignee_agent_member_id,
        assignee_name: row.assignee_name,
        source_thread: source,
        related_threads: related,
        result_message,
        close_reason_code: match row.close_reason_code.as_deref() {
            None => None,
            Some("invalid") => Some(CloseReasonCode::Invalid),
            Some("duplicate") => Some(CloseReasonCode::Duplicate),
            Some("not_needed") => Some(CloseReasonCode::NotNeeded),
            Some("obsolete") => Some(CloseReasonCode::Obsolete),
            Some("other") => Some(CloseReasonCode::Other),
            Some(_) => return Err(ApiError::internal()),
        },
        close_reason_note: row.close_reason_note,
        current_run,
        recent_runs: runs,
        session_continuity: unavailable_continuity(),
        runtime_issue_code: None,
        created_at: timestamp(row.created_at),
        updated_at: timestamp(row.updated_at),
        finished_at: optional_timestamp(row.finished_at),
    })
}

pub(super) async fn run_projection(
    queries: &PostgresQueries,
    run_id: Uuid,
) -> Result<RunResponse, ApiError> {
    let row = queries
        .run(run_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    let focus = thread_reference(
        queries,
        row.focus_thread_id,
        thread_relation(&row.relation)?,
    )
    .await?;
    Ok(RunResponse {
        id: run_id,
        task_id: row.task_id,
        agent_member_id: row.agent_id,
        agent_name: row.agent_name,
        focus,
        status: match row.status.as_str() {
            "dispatched" => RunStatus::Dispatched,
            "working" => RunStatus::Working,
            "completed" => RunStatus::Completed,
            "yielded" => RunStatus::Yielded,
            "failed" => RunStatus::Failed,
            "canceled" => RunStatus::Canceled,
            _ => return Err(ApiError::internal()),
        },
        outcome: match row.outcome_code.as_deref() {
            None => None,
            Some("completed") => Some(RunOutcome::Completed),
            Some("yielded") => Some(RunOutcome::Yielded),
            Some("failed") => Some(RunOutcome::Failed),
            Some("canceled") => Some(RunOutcome::Canceled),
            Some(_) => return Err(ApiError::internal()),
        },
        continuation_note: row.continuation_note,
        error_code: row.error_code,
        started_at: optional_timestamp(row.started_at),
        finished_at: optional_timestamp(row.finished_at),
    })
}

pub(super) fn thread_relation(code: &str) -> Result<ThreadRelation, ApiError> {
    match code {
        "source" => Ok(ThreadRelation::Source),
        "related" => Ok(ThreadRelation::Related),
        _ => Err(ApiError::internal()),
    }
}

pub(super) async fn thread_reference(
    queries: &PostgresQueries,
    thread_id: Uuid,
    relation: ThreadRelation,
) -> Result<ThreadReferenceResponse, ApiError> {
    let row: ThreadReferenceRow = queries
        .thread_reference(thread_id)
        .await
        .map_err(application_error)?;
    Ok(ThreadReferenceResponse {
        id: thread_id,
        root_message_id: row.id,
        channel_id: row.channel_id,
        channel_slug: row.slug,
        root_message_seq: u64::try_from(row.channel_seq).map_err(|_| ApiError::internal())?,
        relation,
    })
}

pub(super) fn timestamp(value: OffsetDateTime) -> String {
    value
        .format(&time::format_description::well_known::Rfc3339)
        .expect("OffsetDateTime formats as RFC3339")
}

pub(super) fn optional_timestamp(value: Option<OffsetDateTime>) -> Option<String> {
    value.map(timestamp)
}

#[derive(serde::Deserialize)]
pub(super) struct InboxQuery {
    /// Absent reads the queue. `dead` reads retired Items, which a governor needs to requeue them.
    pub(super) status: Option<String>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_preset_space_accents() {
        for preset in SPACE_ACCENTS {
            assert_eq!(normalize_space_accent(preset).as_deref(), Some(preset));
            assert_eq!(
                normalize_space_accent(&preset.to_lowercase()).as_deref(),
                Some(preset)
            );
            assert_eq!(
                normalize_space_accent(&format!("  {preset}  ")).as_deref(),
                Some(preset)
            );
        }
    }

    #[test]
    fn rejects_unknown_space_accents() {
        assert_eq!(normalize_space_accent("#123456"), None);
        assert_eq!(normalize_space_accent("pink"), None);
        assert_eq!(normalize_space_accent(""), None);
    }
}
