use super::*;

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
    pub(super) name: String,
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

pub(super) fn space_row(row: &sqlx::postgres::PgRow) -> SpaceResponse {
    space_response(
        row.get("id"),
        row.get("name"),
        row.get("slug"),
        row.get("accent"),
        row.get("owner_member_id"),
        row.get("current_member_id"),
        row.get("general_channel_id"),
    )
}

pub(super) fn channel_row(
    row: &sqlx::postgres::PgRow,
    creator: Uuid,
) -> Result<ChannelResponse, ApiError> {
    let kind = match row.get::<&str, _>("kind") {
        "public" => ChannelKindCode::Public,
        "private" => ChannelKindCode::Private,
        _ => return Err(ApiError::internal()),
    };
    let slug: String = row.try_get("slug").map_err(|_| ApiError::internal())?;
    let topic: Option<String> = row.get("topic");
    Ok(ChannelResponse {
        id: row.get("id"),
        space_id: row.get("space_id"),
        name: topic.clone().unwrap_or_else(|| slug.clone()),
        slug,
        topic,
        kind,
        created_by_member_id: creator,
        joined: row.get("joined"),
        archived_at: optional_timestamp(row.get("archived_at")),
    })
}

pub(super) async fn member_row(
    pool: &PgPool,
    row: &sqlx::postgres::PgRow,
) -> Result<MemberResponse, ApiError> {
    let id: Uuid = row.get("id");
    let permissions = sqlx::query_scalar::<_, String>(
        "SELECT action_code FROM member_permissions WHERE member_id=$1 ORDER BY action_code",
    )
    .bind(id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    Ok(MemberResponse {
        id,
        kind: match row.get::<&str, _>("kind") {
            "human" => MemberKindCode::Human,
            "agent" => MemberKindCode::Agent,
            _ => return Err(ApiError::internal()),
        },
        display_name: row.get("display_name"),
        access_level: match row.get::<&str, _>("access_level") {
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
    pool: &PgPool,
    thread_id: Uuid,
) -> Result<Option<MessageTaskSummary>, ApiError> {
    let row = sqlx::query(
        "SELECT t.id, t.seq, t.title, t.status, t.assignee_agent_member_id, \
                m.display_name AS assignee_name, \
                (SELECT r.focus_thread_id FROM agent_runs r \
                 WHERE r.task_id = t.id AND r.status IN ('dispatched','working') \
                 ORDER BY r.created_at DESC LIMIT 1) AS active_focus_thread_id \
         FROM tasks t LEFT JOIN members m ON m.id = t.assignee_agent_member_id \
         WHERE t.source_thread_id = $1 OR EXISTS (SELECT 1 FROM task_threads tt \
             WHERE tt.task_id = t.id AND tt.thread_id = $1) \
         ORDER BY (t.source_thread_id = $1) DESC, t.created_at DESC LIMIT 1",
    )
    .bind(thread_id)
    .fetch_optional(pool)
    .await
    .map_err(map_sqlx)?;
    let Some(row) = row else { return Ok(None) };
    let active_focus: Option<Uuid> = row.get("active_focus_thread_id");
    Ok(Some(MessageTaskSummary {
        id: row.get("id"),
        seq: u64::try_from(row.get::<i64, _>("seq")).map_err(|_| ApiError::internal())?,
        title: row.get("title"),
        status: task_status_code(row.get("status"))?,
        assignee_agent_member_id: row.get("assignee_agent_member_id"),
        assignee_name: row.get("assignee_name"),
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
    pool: &PgPool,
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
    let rows =
        sqlx::query("SELECT id, seq, title, status FROM tasks WHERE space_id=$1 AND seq = ANY($2)")
            .bind(space_id)
            .bind(&seqs)
            .fetch_all(pool)
            .await
            .map_err(map_sqlx)?;
    let mut refs = rows
        .iter()
        .map(|row| {
            Ok(MessageTaskRefResponse {
                seq: u64::try_from(row.get::<i64, _>("seq")).map_err(|_| ApiError::internal())?,
                task_id: row.get("id"),
                title: row.get("title"),
                status: task_status_code(row.get("status"))?,
            })
        })
        .collect::<Result<Vec<_>, ApiError>>()?;
    refs.sort_by_key(|task_ref| task_ref.seq);
    Ok(refs)
}

pub(super) async fn message_row(
    pool: &PgPool,
    row: &sqlx::postgres::PgRow,
) -> Result<MessageResponse, ApiError> {
    let id: Uuid = row.get("id");
    let space_id: Uuid = row.get("space_id");
    let placement_root = matches!(row.get::<&str, _>("placement"), "root");
    let author = sqlx::query("SELECT id,kind,display_name FROM members WHERE id=$1")
        .bind(row.get::<Uuid, _>("author_member_id"))
        .fetch_one(pool)
        .await
        .map_err(map_sqlx)?;
    let replies: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM messages WHERE thread_id=$1 AND placement='reply'",
    )
    .bind(row.get::<Uuid, _>("thread_id"))
    .fetch_one(pool)
    .await
    .map_err(map_sqlx)?;
    let attachments=sqlx::query("SELECT a.* FROM attachments a JOIN message_attachments ma ON ma.attachment_id=a.id WHERE ma.message_id=$1 ORDER BY ma.position").bind(id).fetch_all(pool).await.map_err(map_sqlx)?;
    let mut attachment_views = Vec::with_capacity(attachments.len());
    for attachment in &attachments {
        attachment_views.push(attachment_row(attachment)?);
    }
    let attention_failures = sqlx::query(
        "SELECT member_id,last_error_code FROM inbox_items WHERE message_id=$1 \
         AND last_error_code IS NOT NULL ORDER BY member_id",
    )
    .bind(id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?
    .iter()
    .map(|failure| AttentionFailureResponse {
        agent_member_id: failure.get("member_id"),
        error_code: failure.get("last_error_code"),
        retrying: true,
    })
    .collect::<Vec<_>>();
    let mentions = sqlx::query_scalar::<_, Uuid>(
        "SELECT member_id FROM message_mentions WHERE message_id=$1 ORDER BY member_id",
    )
    .bind(id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let task = if placement_root {
        message_task_summary(pool, row.get::<Uuid, _>("thread_id")).await?
    } else {
        None
    };
    let task_refs = task_refs_for_message(
        pool,
        space_id,
        row.get::<Option<String>, _>("body_markdown").as_deref(),
    )
    .await?;
    let content = match row.get::<&str, _>("content_kind") {
        "text" => MessageContentResponse::Text {
            body_markdown: row
                .get::<Option<String>, _>("body_markdown")
                .unwrap_or_default(),
        },
        "channel_created" => {
            let target = sqlx::query("SELECT id,slug,topic,archived_at FROM channels WHERE id=$1")
                .bind(row.get::<Uuid, _>("action_channel_id"))
                .fetch_one(pool)
                .await
                .map_err(map_sqlx)?;
            let slug: String = target.get("slug");
            MessageContentResponse::ChannelCreated {
                channel: ActionChannelResponse {
                    id: target.get("id"),
                    name: target
                        .get::<Option<String>, _>("topic")
                        .unwrap_or_else(|| slug.clone()),
                    slug,
                    available: target
                        .get::<Option<OffsetDateTime>, _>("archived_at")
                        .is_none(),
                },
            }
        }
        "agent_created" => {
            let target = sqlx::query("SELECT a.member_id,a.lifecycle,m.display_name,m.retired_at FROM agents a JOIN members m ON m.id=a.member_id WHERE a.member_id=$1")
                .bind(row.get::<Uuid,_>("action_agent_member_id"))
                .fetch_one(pool)
                .await
                .map_err(map_sqlx)?;
            MessageContentResponse::AgentCreated {
                agent: ActionAgentResponse {
                    member_id: target.get("member_id"),
                    name: target.get("display_name"),
                    lifecycle: match target.get::<&str, _>("lifecycle") {
                        "suspended" => AgentLifecycle::Suspended,
                        "retired" => AgentLifecycle::Retired,
                        _ => AgentLifecycle::Active,
                    },
                    available: target
                        .get::<Option<OffsetDateTime>, _>("retired_at")
                        .is_none(),
                },
            }
        }
        "system_notice" => MessageContentResponse::SystemNotice {
            body_markdown: row
                .get::<Option<String>, _>("body_markdown")
                .unwrap_or_default(),
        },
        _ => return Err(ApiError::internal()),
    };
    Ok(MessageResponse {
        id,
        channel_id: row.get("channel_id"),
        thread_id: row.get("thread_id"),
        seq: u64::try_from(row.get::<i64, _>("channel_seq")).map_err(|_| ApiError::internal())?,
        placement: match row.get::<&str, _>("placement") {
            "root" => MessagePlacement::Root,
            "reply" => MessagePlacement::Reply,
            _ => return Err(ApiError::internal()),
        },
        author: MessageAuthor {
            id: author.get("id"),
            kind: match author.get::<&str, _>("kind") {
                "human" => MemberKindCode::Human,
                "agent" => MemberKindCode::Agent,
                _ => return Err(ApiError::internal()),
            },
            display_name: author.get("display_name"),
        },
        content,
        mentions,
        mention_all: row.get("mention_all"),
        attachments: attachment_views,
        reply_count: u64::try_from(replies).map_err(|_| ApiError::internal())?,
        task,
        task_refs,
        attention_failures,
        created_at: timestamp(row.get("created_at")),
        edited_at: optional_timestamp(row.get("edited_at")),
        deleted_at: optional_timestamp(row.get("deleted_at")),
    })
}

pub(super) fn attachment_row(row: &sqlx::postgres::PgRow) -> Result<AttachmentResponse, ApiError> {
    Ok(AttachmentResponse {
        id: row.get("id"),
        space_id: row.get("space_id"),
        uploader_member_id: row.get("uploader_member_id"),
        original_name: row.get("name"),
        media_type: row.get("media_type"),
        size: row
            .get::<Option<i64>, _>("length")
            .map(u64::try_from)
            .transpose()
            .map_err(|_| ApiError::internal())?,
        sha256: row.get::<Option<Vec<u8>>, _>("sha256").map(hex::encode),
        status: match row.get::<&str, _>("status") {
            "uploading" => AttachmentStatus::Uploading,
            "ready" => AttachmentStatus::Ready,
            "deleted" => AttachmentStatus::Deleted,
            _ => return Err(ApiError::internal()),
        },
        upload_path: None,
        download_path: None,
        created_at: timestamp(row.get("created_at")),
    })
}

pub(super) async fn task_projection(
    pool: &PgPool,
    task_id: Uuid,
) -> Result<TaskResponse, ApiError> {
    let row=sqlx::query("SELECT t.*,creator.display_name AS creator_name,assignee.display_name AS assignee_name FROM tasks t JOIN members creator ON creator.id=t.creator_member_id LEFT JOIN members assignee ON assignee.id=t.assignee_agent_member_id WHERE t.id=$1").bind(task_id).fetch_optional(pool).await.map_err(map_sqlx)?.ok_or_else(ApiError::not_found)?;
    let source =
        thread_reference(pool, row.get("source_thread_id"), ThreadRelation::Source).await?;
    let related_ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT thread_id FROM task_threads WHERE task_id=$1 ORDER BY linked_at",
    )
    .bind(task_id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let mut related = Vec::with_capacity(related_ids.len());
    for id in related_ids {
        related.push(thread_reference(pool, id, ThreadRelation::Related).await?);
    }
    let result_message = if let Some(message_id) = row.get::<Option<Uuid>, _>("result_message_id") {
        let message = sqlx::query("SELECT * FROM messages WHERE id=$1")
            .bind(message_id)
            .fetch_one(pool)
            .await
            .map_err(map_sqlx)?;
        Some(message_row(pool, &message).await?)
    } else {
        None
    };
    let run_ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT id FROM agent_runs WHERE task_id=$1 ORDER BY created_at DESC,id DESC LIMIT 20",
    )
    .bind(task_id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let mut runs = Vec::with_capacity(run_ids.len());
    for run_id in run_ids {
        runs.push(run_projection(pool, run_id).await?);
    }
    let current_run = runs.iter().find(|run| run.outcome.is_none()).cloned();
    Ok(TaskResponse {
        id: task_id,
        seq: u64::try_from(row.get::<i64, _>("seq")).map_err(|_| ApiError::internal())?,
        space_id: row.get("space_id"),
        title: row.get("title"),
        status: match row.get::<&str, _>("status") {
            "todo" => TaskStatus::Todo,
            "in_progress" => TaskStatus::InProgress,
            "in_review" => TaskStatus::InReview,
            "done" => TaskStatus::Done,
            "closed" => TaskStatus::Closed,
            _ => return Err(ApiError::internal()),
        },
        creator_member_id: row.get("creator_member_id"),
        creator_name: row.get("creator_name"),
        assignee_agent_member_id: row.get("assignee_agent_member_id"),
        assignee_name: row.get("assignee_name"),
        source_thread: source,
        related_threads: related,
        result_message,
        close_reason_code: match row.get::<Option<&str>, _>("close_reason_code") {
            None => None,
            Some("invalid") => Some(CloseReasonCode::Invalid),
            Some("duplicate") => Some(CloseReasonCode::Duplicate),
            Some("not_needed") => Some(CloseReasonCode::NotNeeded),
            Some("obsolete") => Some(CloseReasonCode::Obsolete),
            Some("other") => Some(CloseReasonCode::Other),
            Some(_) => return Err(ApiError::internal()),
        },
        close_reason_note: row.get("close_reason_note"),
        current_run,
        recent_runs: runs,
        session_continuity: unavailable_continuity(),
        runtime_issue_code: None,
        created_at: timestamp(row.get("created_at")),
        updated_at: timestamp(row.get("updated_at")),
        finished_at: optional_timestamp(row.get("finished_at")),
    })
}

pub(super) async fn run_projection(pool: &PgPool, run_id: Uuid) -> Result<RunResponse, ApiError> {
    let row = sqlx::query(
        "SELECT r.*,m.display_name AS agent_name,\
                CASE WHEN task.id IS NULL OR task.source_thread_id=r.focus_thread_id THEN 'source' ELSE 'related' END AS relation \
         FROM agent_runs r JOIN members m ON m.id=r.agent_id \
         LEFT JOIN tasks task ON task.id=r.task_id WHERE r.id=$1",
    )
    .bind(run_id)
    .fetch_optional(pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    let focus = thread_reference(
        pool,
        row.get("focus_thread_id"),
        thread_relation(row.get("relation"))?,
    )
    .await?;
    Ok(RunResponse {
        id: run_id,
        task_id: row.get("task_id"),
        agent_member_id: row.get("agent_id"),
        agent_name: row.get("agent_name"),
        focus,
        status: match row.get::<&str, _>("status") {
            "dispatched" => RunStatus::Dispatched,
            "working" => RunStatus::Working,
            "completed" => RunStatus::Completed,
            "yielded" => RunStatus::Yielded,
            "failed" => RunStatus::Failed,
            "canceled" => RunStatus::Canceled,
            _ => return Err(ApiError::internal()),
        },
        outcome: match row.get::<Option<&str>, _>("outcome_code") {
            None => None,
            Some("completed") => Some(RunOutcome::Completed),
            Some("yielded") => Some(RunOutcome::Yielded),
            Some("failed") => Some(RunOutcome::Failed),
            Some("canceled") => Some(RunOutcome::Canceled),
            Some(_) => return Err(ApiError::internal()),
        },
        continuation_note: row.get("continuation_note"),
        error_code: row.get("error_code"),
        started_at: optional_timestamp(row.get("started_at")),
        finished_at: optional_timestamp(row.get("finished_at")),
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
    pool: &PgPool,
    thread_id: Uuid,
    relation: ThreadRelation,
) -> Result<ThreadReferenceResponse, ApiError> {
    let row=sqlx::query("SELECT m.id,m.channel_id,c.slug,m.channel_seq FROM messages m JOIN channels c ON c.id=m.channel_id WHERE m.id=$1 AND m.placement='root'").bind(thread_id).fetch_one(pool).await.map_err(map_sqlx)?;
    Ok(ThreadReferenceResponse {
        id: thread_id,
        root_message_id: row.get("id"),
        channel_id: row.get("channel_id"),
        channel_slug: row.get::<Option<String>, _>("slug"),
        root_message_seq: u64::try_from(row.get::<i64, _>("channel_seq"))
            .map_err(|_| ApiError::internal())?,
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
