use axum::{
    Json,
    extract::{Path, State},
    http::{HeaderMap, StatusCode},
};
use serde::Deserialize;
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, attachment, channel, idempotency, message};
use crate::local_protocol::AgentAction;

#[derive(Deserialize)]
pub struct AgentActionRequest {
    agent_member_id: Uuid,
    run_id: Uuid,
    action: AgentAction,
}

pub async fn agent_action(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    Json(request): Json<AgentActionRequest>,
) -> Result<Json<serde_json::Value>, ApiError> {
    super::computer_auth::require_active_run(
        &state,
        &headers,
        computer_id,
        request.agent_member_id,
        request.run_id,
    )
    .await?;
    let data = match request.action {
        AgentAction::MemberList { query } => {
            agent_member_list(&state.database, request.agent_member_id, query.as_deref()).await?
        }
        AgentAction::ChannelList => {
            agent_channel_list(&state.database, request.agent_member_id).await?
        }
        AgentAction::InboxCurrent => {
            agent_inbox_current(&state.database, request.agent_member_id, request.run_id).await?
        }
        AgentAction::InboxShow { inbox_item_id } => {
            agent_inbox_show(
                &state.database,
                request.agent_member_id,
                request.run_id,
                inbox_item_id,
            )
            .await?
        }
        AgentAction::ChannelRead {
            address,
            before,
            after,
            around,
            limit,
        } => {
            agent_channel_read(
                &state.database,
                request.agent_member_id,
                &address,
                before,
                after,
                around,
                limit,
            )
            .await?
        }
        AgentAction::ChannelCreate {
            slug,
            name,
            private,
            idempotency_key,
        } => serde_json::to_value(
            channel::create_for_agent(
                &state.database,
                request.agent_member_id,
                channel::CreateChannelRequest {
                    name,
                    slug,
                    kind: if private { "private" } else { "public" }.to_owned(),
                    topic: None,
                    agent_member_ids: Vec::new(),
                },
                idempotency_key,
            )
            .await?,
        )
        .map_err(|_| ApiError::Internal)?,
        AgentAction::ThreadRead {
            address,
            after,
            limit,
            include_channel,
        } => {
            agent_thread_read(
                &state.database,
                request.agent_member_id,
                &address,
                after,
                limit,
                include_channel,
            )
            .await?
        }
        AgentAction::MessageSend {
            address,
            body_markdown,
            based_on,
            handle_inbox_item_id,
            attachment_ids,
            idempotency_key,
        } => {
            agent_message_send(
                &state.database,
                request.agent_member_id,
                request.run_id,
                AgentMessageSend {
                    address,
                    body_markdown,
                    based_on,
                    handle_inbox_item_id,
                    attachment_ids,
                    idempotency_key,
                },
            )
            .await?
        }
        AgentAction::InboxAck {
            inbox_item_ids,
            reason,
            idempotency_key,
        } => {
            agent_inbox_ack(
                &state.database,
                request.agent_member_id,
                request.run_id,
                &inbox_item_ids,
                &reason,
                idempotency_key,
            )
            .await?
        }
        AgentAction::InboxDefer {
            inbox_item_ids,
            until,
            idempotency_key,
        } => {
            agent_inbox_defer(
                &state.database,
                request.agent_member_id,
                request.run_id,
                &inbox_item_ids,
                until,
                idempotency_key,
            )
            .await?
        }
        AgentAction::AgentCreate {
            name,
            role_text,
            computer_id,
            driver_kind,
            idempotency_key,
        } => serde_json::to_value(
            super::approval::request_agent_create(
                &state.database,
                request.agent_member_id,
                super::agent_registry::CreateAgentRequest {
                    computer_id,
                    name,
                    handle: None,
                    role_text,
                    access_level: "member".to_owned(),
                    driver_kind,
                },
                idempotency_key,
            )
            .await?,
        )
        .map_err(|_| ApiError::Internal)?,
        AgentAction::AttachmentUpload { .. }
        | AgentAction::AttachmentDownload { .. }
        | AgentAction::AttachmentInfo { .. } => {
            return Err(ApiError::validation(
                "invalid_attachment_transport",
                "Attachment actions must use the streaming Agent Attachment API",
            ));
        }
    };
    Ok(Json(data))
}

async fn agent_member_list(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    query: Option<&str>,
) -> Result<serde_json::Value, ApiError> {
    if query.is_some_and(|query| query.chars().count() > 100) {
        return Err(ApiError::validation(
            "invalid_member_query",
            "Member query must contain at most 100 characters",
        ));
    }
    let members: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', members.id, 'kind', members.kind, \
            'display_name', members.display_name, 'handle', members.handle, \
            'access_level', members.access_level) FROM members \
         WHERE members.space_id = (SELECT space_id FROM agents WHERE member_id = $1) \
           AND members.retired_at IS NULL AND ($2::text IS NULL \
             OR members.display_name ILIKE '%' || $2 || '%' OR members.handle ILIKE '%' || $2 || '%') \
         ORDER BY lower(members.display_name), members.id LIMIT 100",
    )
    .bind(agent_id)
    .bind(query.map(str::trim).filter(|query| !query.is_empty()))
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    Ok(serde_json::json!({ "members": members }))
}

async fn agent_channel_list(
    database: &sqlx::PgPool,
    agent_id: Uuid,
) -> Result<serde_json::Value, ApiError> {
    let channels: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', channels.id, 'address', '#' || channels.slug::text, \
            'kind', channels.kind, 'name', channels.name, 'topic', channels.topic, \
            'joined', EXISTS(SELECT 1 FROM channel_members own \
                WHERE own.channel_id = channels.id AND own.member_id = $1)) \
         FROM channels WHERE channels.space_id = (SELECT space_id FROM agents WHERE member_id = $1) \
           AND channels.kind != 'direct' AND channels.archived_at IS NULL \
           AND (channels.kind = 'public' OR EXISTS(SELECT 1 FROM channel_members own \
                WHERE own.channel_id = channels.id AND own.member_id = $1)) \
         ORDER BY lower(channels.name)",
    )
    .bind(agent_id)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    let dms: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('channel_id', direct_channels.channel_id, \
            'address', '@' || other.handle, 'member', jsonb_build_object('id', other.id, \
                'kind', other.kind, 'display_name', other.display_name, 'handle', other.handle)) \
         FROM direct_channels JOIN members other ON other.id = CASE \
             WHEN direct_channels.member_low_id = $1 THEN direct_channels.member_high_id \
             ELSE direct_channels.member_low_id END \
         WHERE $1 IN (direct_channels.member_low_id, direct_channels.member_high_id) \
         ORDER BY lower(other.display_name)",
    )
    .bind(agent_id)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    Ok(serde_json::json!({ "channels": channels, "direct_messages": dms }))
}

async fn agent_inbox_current(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
) -> Result<serde_json::Value, ApiError> {
    let items: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', inbox_items.id, 'kind', inbox_items.kind, 'priority', inbox_items.priority, \
            'channel_id', inbox_items.channel_id, 'channel_slug', channels.slug::text, \
            'thread_id', inbox_items.thread_id, 'message_id', inbox_items.message_id, \
            'sender_member_id', senders.id, 'sender_display_name', senders.display_name, \
            'sender_handle', senders.handle, \
            'address', CASE WHEN channels.kind = 'direct' THEN '@' || senders.handle \
                WHEN inbox_items.thread_id IS NOT NULL \
                    THEN '#' || channels.slug::text || ':' || inbox_items.thread_id::text \
                ELSE '#' || channels.slug::text END, \
            'summary', CASE WHEN messages.deleted_at IS NULL THEN left(messages.body_markdown, 160) \
                            ELSE 'Message 已删除' END, \
            'status', inbox_items.status, 'available_at', inbox_items.available_at, \
            'created_at', inbox_items.created_at) \
         FROM agent_run_inbox_items \
         JOIN inbox_items ON inbox_items.id = agent_run_inbox_items.inbox_item_id \
         LEFT JOIN channels ON channels.id = inbox_items.channel_id \
         LEFT JOIN messages ON messages.id = inbox_items.message_id \
         LEFT JOIN members senders ON senders.id = messages.author_member_id \
         WHERE agent_run_inbox_items.run_id = $1 AND inbox_items.member_id = $2 \
           AND inbox_items.status = 'leased' AND inbox_items.lease_id = agent_run_inbox_items.lease_id \
         ORDER BY CASE inbox_items.priority WHEN 'hard' THEN 0 ELSE 1 END, inbox_items.created_at",
    )
    .bind(run_id)
    .bind(agent_id)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    Ok(serde_json::json!({ "run_id": run_id, "items": items }))
}

async fn agent_inbox_show(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    item_id: Uuid,
) -> Result<serde_json::Value, ApiError> {
    sqlx::query_scalar(
        "SELECT jsonb_build_object('id', inbox_items.id, 'kind', inbox_items.kind, \
            'priority', inbox_items.priority, 'channel_id', inbox_items.channel_id, \
            'address', CASE WHEN channels.kind = 'direct' THEN '@' || senders.handle \
                WHEN inbox_items.thread_id IS NOT NULL \
                    THEN '#' || channels.slug::text || ':' || inbox_items.thread_id::text \
                ELSE '#' || channels.slug::text END, 'thread_id', inbox_items.thread_id, \
            'message_id', inbox_items.message_id, 'sender', jsonb_build_object('id', senders.id, \
                'kind', senders.kind, 'display_name', senders.display_name, 'handle', senders.handle), \
            'summary', CASE WHEN messages.deleted_at IS NULL THEN left(messages.body_markdown, 160) \
                ELSE 'Message 已删除' END, 'status', inbox_items.status, \
            'available_at', inbox_items.available_at, 'created_at', inbox_items.created_at) \
         FROM agent_run_inbox_items JOIN inbox_items \
           ON inbox_items.id = agent_run_inbox_items.inbox_item_id \
         LEFT JOIN channels ON channels.id = inbox_items.channel_id \
         LEFT JOIN messages ON messages.id = inbox_items.message_id \
         LEFT JOIN members senders ON senders.id = messages.author_member_id \
         WHERE agent_run_inbox_items.run_id = $1 AND inbox_items.id = $2 \
           AND inbox_items.member_id = $3 AND inbox_items.status = 'leased' \
           AND inbox_items.lease_id = agent_run_inbox_items.lease_id",
    )
    .bind(run_id)
    .bind(item_id)
    .bind(agent_id)
    .fetch_optional(database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("inbox_item_not_found", "Inbox Item is not claimed by this run"))
}

async fn agent_channel_read(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
    before: Option<i64>,
    after: Option<i64>,
    around: Option<Uuid>,
    limit: i64,
) -> Result<serde_json::Value, ApiError> {
    let cursor_count = usize::from(before.is_some())
        + usize::from(after.is_some())
        + usize::from(around.is_some());
    if !(1..=100).contains(&limit)
        || before.is_some_and(|value| value <= 0)
        || after.is_some_and(|value| value < 0)
        || cursor_count > 1
    {
        return Err(ApiError::validation(
            "invalid_pagination",
            "Message limit must be 1 to 100 and before, after, and around are mutually exclusive",
        ));
    }
    let (channel_id, display_address) = resolve_agent_address(database, agent_id, address).await?;
    let snapshot: i64 = sqlx::query_scalar("SELECT next_seq - 1 FROM channels WHERE id = $1")
        .bind(channel_id)
        .fetch_one(database)
        .await
        .map_err(ApiError::database)?;
    let around_seq: Option<i64> = if let Some(message_id) = around {
        Some(
            sqlx::query_scalar::<_, i64>(
                "SELECT channel_seq FROM messages WHERE id = $1 AND channel_id = $2 \
                 AND thread_id IS NULL",
            )
            .bind(message_id)
            .bind(channel_id)
            .fetch_optional(database)
            .await
            .map_err(ApiError::database)?
            .ok_or_else(|| {
                ApiError::not_found(
                    "message_not_found",
                    "Around Message was not found on this Channel main timeline",
                )
            })?,
        )
    } else {
        None
    };
    let mut messages: Vec<serde_json::Value> = sqlx::query_scalar(
        "WITH candidates AS (SELECT messages.id, messages.channel_seq, \
            messages.author_member_id, messages.body_markdown, messages.created_at, \
            messages.edited_at, messages.deleted_at \
         FROM messages WHERE messages.channel_id = $1 AND messages.thread_id IS NULL \
           AND messages.channel_seq <= $2 \
           AND ($3::bigint IS NULL OR messages.channel_seq < $3) \
           AND ($4::bigint IS NULL OR messages.channel_seq > $4) \
         ORDER BY CASE WHEN $5::bigint IS NULL THEN NULL \
                       ELSE abs(messages.channel_seq - $5) END, \
                  CASE WHEN $4::bigint IS NOT NULL THEN messages.channel_seq END ASC, \
                  CASE WHEN $4::bigint IS NULL AND $5::bigint IS NULL \
                       THEN messages.channel_seq END DESC, \
                  messages.channel_seq \
         LIMIT $6) \
         SELECT jsonb_build_object( \
            'id', messages.id, 'seq', messages.channel_seq, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle), \
            'address', $7::text, \
            'body_markdown', CASE WHEN messages.deleted_at IS NULL THEN messages.body_markdown \
                                  ELSE 'Message 已删除' END, \
            'created_at', messages.created_at, 'edited_at', messages.edited_at) \
         FROM candidates messages JOIN members ON members.id = messages.author_member_id",
    )
    .bind(channel_id)
    .bind(snapshot)
    .bind(before)
    .bind(after)
    .bind(around_seq)
    .bind(limit)
    .bind(&display_address)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    messages.sort_by_key(|message| message.get("seq").and_then(serde_json::Value::as_i64));
    let first_seq = messages
        .first()
        .and_then(|message| message.get("seq"))
        .and_then(serde_json::Value::as_i64);
    let last_seq = messages
        .last()
        .and_then(|message| message.get("seq"))
        .and_then(serde_json::Value::as_i64);
    let first_boundary = first_seq.unwrap_or_else(|| {
        after
            .map(|value| value.saturating_add(1))
            .or(before)
            .unwrap_or_else(|| snapshot.saturating_add(1))
    });
    let last_boundary = last_seq.unwrap_or_else(|| {
        after
            .or_else(|| before.map(|value| value.saturating_sub(1)))
            .unwrap_or(0)
    });
    let has_more_before: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM messages WHERE channel_id = $1 AND thread_id IS NULL \
         AND channel_seq < $2)",
    )
    .bind(channel_id)
    .bind(first_boundary)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)?;
    let has_more_after: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM messages WHERE channel_id = $1 AND thread_id IS NULL \
         AND channel_seq > $2 AND channel_seq <= $3)",
    )
    .bind(channel_id)
    .bind(last_boundary)
    .bind(snapshot)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)?;
    enrich_agent_messages(database, &mut messages).await?;
    Ok(serde_json::json!({
        "address": display_address,
        "channel_id": channel_id,
        "thread_id": null,
        "snapshot_channel_seq": snapshot,
        "messages": messages,
        "has_more_before": has_more_before,
        "has_more_after": has_more_after,
    }))
}

async fn agent_thread_read(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
    after: Option<i64>,
    limit: i64,
    include_channel: i64,
) -> Result<serde_json::Value, ApiError> {
    if !(1..=100).contains(&limit)
        || !(0..=100).contains(&include_channel)
        || after.is_some_and(|value| value < 0)
    {
        return Err(ApiError::validation(
            "invalid_pagination",
            "Thread pagination is invalid",
        ));
    }
    let raw = address.strip_prefix('#').ok_or_else(|| {
        ApiError::validation("invalid_address", "Thread address must use #channel:id")
    })?;
    let (slug, thread_id) = raw
        .rsplit_once(':')
        .and_then(|(slug, id)| id.parse::<i64>().ok().map(|id| (slug, id)))
        .filter(|(slug, id)| !slug.is_empty() && *id > 0)
        .ok_or_else(|| {
            ApiError::validation("invalid_address", "Thread address must use #channel:id")
        })?;
    let access: Option<(Uuid, Uuid, i64)> = sqlx::query_as(
        "SELECT threads.channel_id, threads.root_message_id, channels.next_seq - 1 \
         FROM threads JOIN channels ON channels.id = threads.channel_id \
         JOIN channel_members ON channel_members.channel_id = threads.channel_id \
         WHERE channels.slug = $1 AND threads.thread_id = $2 \
           AND channel_members.member_id = $3",
    )
    .bind(slug)
    .bind(thread_id)
    .bind(agent_id)
    .fetch_optional(database)
    .await
    .map_err(ApiError::database)?;
    let (channel_id, root_message_id, snapshot) = access.ok_or_else(|| {
        ApiError::forbidden(
            "permission_denied",
            "Agent is not a member of this Thread Channel",
        )
    })?;
    let root = agent_message_json_pool(database, root_message_id, address).await?;
    let replies: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', messages.id, 'seq', messages.channel_seq, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle), \
            'address', $5::text, 'body_markdown', CASE WHEN messages.deleted_at IS NULL \
                THEN messages.body_markdown ELSE 'Message 已删除' END, \
            'created_at', messages.created_at, 'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         WHERE messages.channel_id = $1 AND messages.thread_id = $2 \
           AND messages.channel_seq > $3 ORDER BY messages.channel_seq LIMIT $4",
    )
    .bind(channel_id)
    .bind(thread_id)
    .bind(after.unwrap_or(0))
    .bind(limit + 1)
    .bind(address)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    let has_more_after = replies.len() as i64 > limit;
    let mut replies = replies.into_iter().take(limit as usize).collect::<Vec<_>>();
    let root_seq = root
        .get("seq")
        .and_then(serde_json::Value::as_i64)
        .ok_or(ApiError::Internal)?;
    let background: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', messages.id, 'seq', messages.channel_seq, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle), \
            'address', '#' || channels.slug::text, 'body_markdown', messages.body_markdown, \
            'created_at', messages.created_at, 'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         JOIN channels ON channels.id = messages.channel_id \
         WHERE messages.channel_id = $1 AND messages.thread_id IS NULL \
           AND messages.channel_seq < $2 ORDER BY messages.channel_seq DESC LIMIT $3",
    )
    .bind(channel_id)
    .bind(root_seq)
    .bind(include_channel)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    enrich_agent_messages(database, &mut replies).await?;
    let mut background = background.into_iter().rev().collect::<Vec<_>>();
    enrich_agent_messages(database, &mut background).await?;
    Ok(serde_json::json!({
        "address": address,
        "channel_id": channel_id,
        "thread_id": thread_id,
        "snapshot_channel_seq": snapshot,
        "root": root,
        "replies": replies,
        "channel_background": background,
        "has_more_before": false,
        "has_more_after": has_more_after,
    }))
}

async fn resolve_agent_address(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
) -> Result<(Uuid, String), ApiError> {
    if let Some(slug) = address.strip_prefix('#') {
        if slug.is_empty() || slug.contains(':') {
            return Err(ApiError::validation(
                "invalid_address",
                "This command requires a Channel main timeline address",
            ));
        }
        return sqlx::query_as(
            "SELECT channels.id, '#' || channels.slug::text FROM channels \
             JOIN channel_members ON channel_members.channel_id = channels.id \
             WHERE channels.slug = $1 AND channel_members.member_id = $2 \
               AND channels.archived_at IS NULL",
        )
        .bind(slug)
        .bind(agent_id)
        .fetch_optional(database)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| {
            ApiError::forbidden("permission_denied", "Agent is not a member of this Channel")
        });
    }
    if let Some(handle) = address.strip_prefix('@') {
        return sqlx::query_as(
            "SELECT direct_channels.channel_id, '@' || target.handle FROM direct_channels \
             JOIN members target ON target.id = CASE \
                 WHEN direct_channels.member_low_id = $1 THEN direct_channels.member_high_id \
                 ELSE direct_channels.member_low_id END \
             WHERE $1 IN (direct_channels.member_low_id, direct_channels.member_high_id) \
               AND lower(target.handle) = lower($2)",
        )
        .bind(agent_id)
        .bind(handle)
        .fetch_optional(database)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| ApiError::not_found("dm_not_found", "DM was not found"));
    }
    Err(ApiError::validation(
        "invalid_address",
        "Address must use #channel or @member",
    ))
}

struct AgentMessageSend {
    address: String,
    body_markdown: String,
    based_on: Option<i64>,
    handle_inbox_item_id: Option<Uuid>,
    attachment_ids: Vec<Uuid>,
    idempotency_key: Uuid,
}

async fn agent_message_send(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    request: AgentMessageSend,
) -> Result<serde_json::Value, ApiError> {
    let AgentMessageSend {
        address,
        body_markdown,
        based_on,
        handle_inbox_item_id: handle_item_id,
        attachment_ids,
        idempotency_key,
    } = request;
    let body_markdown = body_markdown.trim();
    if !(1..=20_000).contains(&body_markdown.chars().count()) {
        return Err(ApiError::validation(
            "invalid_message_body",
            "Message must contain 1 to 20000 characters",
        ));
    }
    if based_on.is_some_and(|snapshot| snapshot < 0) {
        return Err(ApiError::validation(
            "invalid_context_snapshot",
            "Context snapshot sequence cannot be negative",
        ));
    }
    let (channel_id, display_address, thread_id) =
        resolve_agent_message_target(database, agent_id, &address).await?;
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "run_id": run_id,
        "address": &address,
        "body_markdown": body_markdown,
        "based_on": based_on,
        "handle_inbox_item_id": handle_item_id,
        "attachment_ids": &attachment_ids,
    }))?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let channel: Option<(Uuid, String, Option<OffsetDateTime>, i64)> = sqlx::query_as(
        "SELECT channels.space_id, channels.kind, channels.archived_at, channels.next_seq - 1 \
         FROM channels JOIN channel_members ON channel_members.channel_id = channels.id \
         WHERE channels.id = $1 AND channel_members.member_id = $2 FOR UPDATE OF channels",
    )
    .bind(channel_id)
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, channel_kind, archived_at, current_seq) = channel.ok_or_else(|| {
        ApiError::forbidden("permission_denied", "Channel membership is required")
    })?;
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel is read-only",
        ));
    }
    let scope = format!("agent:{agent_id}:message:send");
    if let Some((_status, response)) = idempotency::begin::<serde_json::Value>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let handled_priority = if let Some(item_id) = handle_item_id {
        let priority: Option<String> = sqlx::query_scalar(
            "SELECT inbox_items.priority FROM agent_run_inbox_items \
             JOIN inbox_items ON inbox_items.id = agent_run_inbox_items.inbox_item_id \
             WHERE agent_run_inbox_items.run_id = $1 \
               AND agent_run_inbox_items.inbox_item_id = $2 \
               AND inbox_items.member_id = $3 AND inbox_items.status = 'leased' \
               AND inbox_items.lease_id = agent_run_inbox_items.lease_id \
               AND inbox_items.lease_expires_at > now()",
        )
        .bind(run_id)
        .bind(item_id)
        .bind(agent_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        let Some(priority) = priority else {
            return Err(ApiError::conflict(
                "inbox_lease_lost",
                "Inbox Item is not leased by this run",
            ));
        };
        Some(priority)
    } else {
        None
    };
    if handled_priority.as_deref() == Some("hard")
        && let Some(snapshot) = based_on
        && snapshot != current_seq
    {
        let details = context_change_details(
            &mut transaction,
            channel_id,
            &display_address,
            snapshot,
            current_seq,
        )
        .await?;
        return Err(ApiError::conflict_with_details(
            "context_changed",
            format!("Channel context changed; latest sequence is {current_seq}"),
            details,
        ));
    }
    let seq: i64 = sqlx::query_scalar(
        "UPDATE channels SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1",
    )
    .bind(channel_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let message_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO messages (id, channel_id, space_id, channel_seq, thread_id, author_member_id, \
         body_markdown, idempotency_key, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
    )
    .bind(message_id)
    .bind(channel_id)
    .bind(space_id)
    .bind(seq)
    .bind(thread_id)
    .bind(agent_id)
    .bind(body_markdown)
    .bind(idempotency_key)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    attachment::attach_to_message(
        &mut transaction,
        message_id,
        space_id,
        agent_id,
        &attachment_ids,
    )
    .await?;
    let mut thread_inbox_changed = false;
    if let Some(thread_id) = thread_id {
        sqlx::query(
            "INSERT INTO thread_subscriptions (channel_id, thread_id, member_id, created_at) \
             VALUES ($1, $2, $3, $4) ON CONFLICT (channel_id, thread_id, member_id) \
             DO UPDATE SET muted_at = NULL",
        )
        .bind(channel_id)
        .bind(thread_id)
        .bind(agent_id)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        if channel_kind != "direct" {
            thread_inbox_changed = super::thread::insert_thread_attention(
                &mut transaction,
                super::thread::ThreadAttention {
                    space_id,
                    channel_id,
                    thread_id,
                    message_id,
                    actor_id: agent_id,
                    seq,
                    reply_to_message_id: None,
                    mentions: &[],
                    channel_kind: &channel_kind,
                    now,
                },
            )
            .await?;
        }
    }
    if let Some(item_id) = handle_item_id {
        let updated = sqlx::query(
            "UPDATE inbox_items SET status = 'handled', handled_by_run_id = $2, handled_at = $3, \
             lease_id = NULL, lease_expires_at = NULL WHERE id = $1 AND member_id = $4 \
             AND status = 'leased'",
        )
        .bind(item_id)
        .bind(run_id)
        .bind(now)
        .bind(agent_id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        if updated.rows_affected() != 1 {
            return Err(ApiError::conflict(
                "inbox_lease_lost",
                "Inbox Item lease changed before Message send",
            ));
        }
        publish_inbox_update(&mut transaction, space_id, agent_id, item_id, now).await?;
    }
    if channel_kind == "direct" {
        let recipient_id: Uuid = sqlx::query_scalar(
            "SELECT member_id FROM channel_members WHERE channel_id = $1 AND member_id <> $2",
        )
        .bind(channel_id)
        .bind(agent_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        let recipient_item_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO inbox_items (id, member_id, space_id, kind, priority, channel_id, \
             message_id, first_seq, last_seq, available_at, created_at) \
             VALUES ($1, $2, $3, 'direct', 'hard', $4, $5, $6, $6, $7, $7)",
        )
        .bind(recipient_item_id)
        .bind(recipient_id)
        .bind(space_id)
        .bind(channel_id)
        .bind(message_id)
        .bind(seq)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        publish_inbox_update(
            &mut transaction,
            space_id,
            recipient_id,
            recipient_item_id,
            now,
        )
        .await?;
    }
    if thread_inbox_changed {
        super::outbox::publish(
            &mut transaction,
            "inbox.changed",
            message_id,
            serde_json::json!({
                "space_id": space_id,
                "channel_id": channel_id,
                "thread_id": thread_id,
            }),
            now,
        )
        .await?;
    }
    if channel_kind != "direct"
        && thread_id.is_none()
        && message::insert_channel_ambient_inbox(
            &mut transaction,
            message::ChannelAmbientInbox {
                space_id,
                channel_id,
                message_id,
                actor_id: agent_id,
                seq,
                hard_recipients: &[],
                now,
            },
        )
        .await?
    {
        super::outbox::publish(
            &mut transaction,
            "inbox.changed",
            message_id,
            serde_json::json!({ "space_id": space_id, "channel_id": channel_id }),
            now,
        )
        .await?;
    }
    super::outbox::publish(
        &mut transaction,
        "message.created",
        message_id,
        serde_json::json!({
            "space_id": space_id,
            "channel_id": channel_id,
            "thread_id": thread_id,
            "message_id": message_id,
            "channel_seq": seq,
        }),
        now,
    )
    .await?;
    let response = agent_message_json(&mut transaction, message_id, &display_address).await?;
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn context_change_details(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    channel_id: Uuid,
    display_address: &str,
    snapshot: i64,
    current_seq: i64,
) -> Result<serde_json::Value, ApiError> {
    let mut changes: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', messages.id, 'seq', messages.channel_seq, \
            'address', CASE WHEN channels.kind = 'direct' THEN $4::text \
                WHEN messages.thread_id IS NULL THEN '#' || channels.slug::text \
                ELSE '#' || channels.slug::text || ':' || messages.thread_id::text END, \
            'thread_id', messages.thread_id, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle)) \
         FROM messages JOIN channels ON channels.id = messages.channel_id \
         JOIN members ON members.id = messages.author_member_id \
         WHERE messages.channel_id = $1 AND messages.channel_seq > $2 \
           AND messages.channel_seq <= $3 ORDER BY messages.channel_seq LIMIT 11",
    )
    .bind(channel_id)
    .bind(snapshot)
    .bind(current_seq)
    .bind(display_address)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let has_more = changes.len() > 10;
    changes.truncate(10);
    Ok(serde_json::json!({
        "snapshot_channel_seq": snapshot,
        "latest_channel_seq": current_seq,
        "changes": changes,
        "has_more": has_more,
    }))
}

async fn resolve_agent_message_target(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
) -> Result<(Uuid, String, Option<i64>), ApiError> {
    if let Some(raw) = address.strip_prefix('#')
        && let Some((slug, thread_id)) = raw
            .rsplit_once(':')
            .and_then(|(slug, id)| id.parse::<i64>().ok().map(|id| (slug, id)))
    {
        if slug.is_empty() || thread_id <= 0 {
            return Err(ApiError::validation(
                "invalid_address",
                "Thread address must use #channel:id",
            ));
        }
        let channel_id: Uuid = sqlx::query_scalar(
            "SELECT channels.id FROM channels \
             JOIN channel_members ON channel_members.channel_id = channels.id \
             JOIN threads ON threads.channel_id = channels.id AND threads.thread_id = $2 \
             WHERE channels.slug = $1 AND channel_members.member_id = $3 \
               AND channels.archived_at IS NULL",
        )
        .bind(slug)
        .bind(thread_id)
        .bind(agent_id)
        .fetch_optional(database)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| {
            ApiError::forbidden(
                "permission_denied",
                "Agent is not a member of this Thread Channel",
            )
        })?;
        return Ok((channel_id, address.to_owned(), Some(thread_id)));
    }
    let (channel_id, display_address) = resolve_agent_address(database, agent_id, address).await?;
    Ok((channel_id, display_address, None))
}

async fn agent_inbox_ack(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    item_ids: &[Uuid],
    reason: &str,
    idempotency_key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    if item_ids.is_empty() || reason.trim().is_empty() || reason.chars().count() > 500 {
        return Err(ApiError::validation(
            "invalid_inbox_ack",
            "Inbox ack requires Items and a reason of at most 500 characters",
        ));
    }
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let scope = format!("agent:{agent_id}:inbox:ack");
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "run_id": run_id,
        "inbox_item_ids": item_ids,
        "reason": reason,
    }))?;
    if let Some((_status, response)) = idempotency::begin::<serde_json::Value>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let rows: Vec<(Uuid, Uuid)> = sqlx::query_as(
        "UPDATE inbox_items SET status = 'handled', handled_by_run_id = $2, handled_at = now(), \
         lease_id = NULL, lease_expires_at = NULL \
         WHERE id = ANY($1) AND member_id = $3 AND status = 'leased' \
           AND EXISTS(SELECT 1 FROM agent_run_inbox_items links WHERE links.run_id = $2 \
             AND links.inbox_item_id = inbox_items.id AND links.lease_id = inbox_items.lease_id) \
         RETURNING id, space_id",
    )
    .bind(item_ids)
    .bind(run_id)
    .bind(agent_id)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if rows.len() != item_ids.len() {
        return Err(ApiError::conflict(
            "inbox_lease_lost",
            "One or more Inbox Items are not leased by this run",
        ));
    }
    let now = OffsetDateTime::now_utc();
    for (item_id, space_id) in &rows {
        publish_inbox_update(&mut transaction, *space_id, agent_id, *item_id, now).await?;
    }
    let response = serde_json::json!({
        "handled_inbox_item_ids": rows.into_iter().map(|row| row.0).collect::<Vec<_>>(),
        "reason_recorded": true,
    });
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn agent_inbox_defer(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    item_ids: &[Uuid],
    until: OffsetDateTime,
    idempotency_key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    if item_ids.is_empty() || until <= OffsetDateTime::now_utc() {
        return Err(ApiError::validation(
            "invalid_inbox_defer",
            "Inbox defer requires Items and a future time",
        ));
    }
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let scope = format!("agent:{agent_id}:inbox:defer");
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "run_id": run_id,
        "inbox_item_ids": item_ids,
        "until": until,
    }))?;
    if let Some((_status, response)) = idempotency::begin::<serde_json::Value>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let rows: Vec<(Uuid, Uuid)> = sqlx::query_as(
        "UPDATE inbox_items SET status = 'deferred', available_at = $4, \
         lease_id = NULL, lease_expires_at = NULL \
         WHERE id = ANY($1) AND member_id = $2 AND status = 'leased' \
           AND EXISTS(SELECT 1 FROM agent_run_inbox_items links WHERE links.run_id = $3 \
             AND links.inbox_item_id = inbox_items.id AND links.lease_id = inbox_items.lease_id) \
         RETURNING id, space_id",
    )
    .bind(item_ids)
    .bind(agent_id)
    .bind(run_id)
    .bind(until)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if rows.len() != item_ids.len() {
        return Err(ApiError::conflict(
            "inbox_lease_lost",
            "One or more Inbox Items are not leased by this run",
        ));
    }
    let now = OffsetDateTime::now_utc();
    for (item_id, space_id) in &rows {
        publish_inbox_update(&mut transaction, *space_id, agent_id, *item_id, now).await?;
    }
    let available_at = until
        .format(&time::format_description::well_known::Rfc3339)
        .map_err(|_| ApiError::Internal)?;
    let response = serde_json::json!({
        "deferred_inbox_item_ids": rows.into_iter().map(|row| row.0).collect::<Vec<_>>(),
        "available_at": available_at,
    });
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

pub(super) async fn publish_inbox_update(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    space_id: Uuid,
    member_id: Uuid,
    item_id: Uuid,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    super::outbox::publish(
        transaction,
        "inbox.changed",
        item_id,
        serde_json::json!({
            "space_id": space_id,
            "member_id": member_id,
            "item_id": item_id,
        }),
        now,
    )
    .await
}

async fn agent_message_json(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    message_id: Uuid,
    address: &str,
) -> Result<serde_json::Value, ApiError> {
    let mut message: serde_json::Value = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', messages.id, 'channel_id', messages.channel_id, 'seq', messages.channel_seq, \
            'address', $2::text, 'author', jsonb_build_object('id', members.id, \
                'kind', members.kind, 'display_name', members.display_name, 'handle', members.handle), \
            'body_markdown', messages.body_markdown, 'mentions', '[]'::jsonb, \
            'created_at', messages.created_at, \
            'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         WHERE messages.id = $1",
    )
    .bind(message_id)
    .bind(address)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let attachments = attachment::attachments_for_message(transaction, message_id).await?;
    message.as_object_mut().ok_or(ApiError::Internal)?.insert(
        "attachments".to_owned(),
        serde_json::to_value(attachments).map_err(|_| ApiError::Internal)?,
    );
    Ok(message)
}

async fn agent_message_json_pool(
    database: &sqlx::PgPool,
    message_id: Uuid,
    address: &str,
) -> Result<serde_json::Value, ApiError> {
    let message: serde_json::Value = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', messages.id, 'channel_id', messages.channel_id, 'seq', messages.channel_seq, \
            'address', $2::text, 'author', jsonb_build_object('id', members.id, \
                'kind', members.kind, 'display_name', members.display_name, 'handle', members.handle), \
            'body_markdown', CASE WHEN messages.deleted_at IS NULL THEN messages.body_markdown \
                ELSE 'Message 已删除' END, 'created_at', messages.created_at, \
            'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         WHERE messages.id = $1",
    )
    .bind(message_id)
    .bind(address)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)?;
    let mut messages = vec![message];
    enrich_agent_messages(database, &mut messages).await?;
    messages.pop().ok_or(ApiError::Internal)
}

async fn enrich_agent_messages(
    database: &sqlx::PgPool,
    messages: &mut [serde_json::Value],
) -> Result<(), ApiError> {
    for message in messages {
        let message_id = message
            .get("id")
            .and_then(serde_json::Value::as_str)
            .and_then(|value| Uuid::parse_str(value).ok())
            .ok_or(ApiError::Internal)?;
        let attachments = attachment::attachments_for_message_pool(database, message_id).await?;
        let mentions: Vec<Uuid> = sqlx::query_scalar(
            "SELECT member_id FROM message_mentions WHERE message_id = $1 ORDER BY member_id",
        )
        .bind(message_id)
        .fetch_all(database)
        .await
        .map_err(ApiError::database)?;
        message.as_object_mut().ok_or(ApiError::Internal)?.insert(
            "attachments".to_owned(),
            serde_json::to_value(attachments).map_err(|_| ApiError::Internal)?,
        );
        message.as_object_mut().ok_or(ApiError::Internal)?.insert(
            "mentions".to_owned(),
            serde_json::to_value(mentions).map_err(|_| ApiError::Internal)?,
        );
    }
    Ok(())
}
