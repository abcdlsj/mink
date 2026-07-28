use axum::{
    Json,
    extract::{Path, Query, State},
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use sqlx::{FromRow, Postgres, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, audit, auth, idempotency, member, outbox};

const STATUSES: &[&str] = &["open", "in_progress", "done", "canceled"];

#[derive(Clone, Debug, Deserialize, Serialize, FromRow, utoipa::ToSchema)]
pub struct TaskResponse {
    pub id: Uuid,
    pub space_id: Uuid,
    pub source_message_id: Uuid,
    pub channel_id: Uuid,
    pub channel_slug: String,
    pub source_seq: i64,
    pub title: String,
    pub status: String,
    pub created_by_member_id: Uuid,
    pub creator_name: String,
    pub assigned_agent_member_id: Option<Uuid>,
    pub assignee_name: Option<String>,
    #[serde(with = "time::serde::rfc3339")]
    pub created_at: OffsetDateTime,
    #[serde(with = "time::serde::rfc3339")]
    pub updated_at: OffsetDateTime,
}

#[derive(Deserialize)]
pub struct ListQuery {
    status: Option<String>,
}

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
    Query(query): Query<ListQuery>,
) -> Result<Json<Vec<TaskResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let actor = member::require_actor(&state.database, user.id, space_id).await?;
    validate_optional_status(query.status.as_deref())?;
    Ok(Json(
        list_visible(&state.database, space_id, actor.id, query.status.as_deref()).await?,
    ))
}

pub(super) async fn list_for_agent(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    status: Option<&str>,
) -> Result<serde_json::Value, ApiError> {
    validate_optional_status(status)?;
    let space_id: Uuid = sqlx::query_scalar(
        "SELECT space_id FROM members WHERE id = $1 AND kind = 'agent' AND retired_at IS NULL",
    )
    .bind(agent_id)
    .fetch_optional(database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| {
        ApiError::forbidden("permission_denied", "Current Agent identity is not active")
    })?;
    let tasks = list_visible(database, space_id, agent_id, status).await?;
    serde_json::to_value(serde_json::json!({ "tasks": tasks })).map_err(|_| ApiError::Internal)
}

async fn list_visible(
    database: &sqlx::PgPool,
    space_id: Uuid,
    member_id: Uuid,
    status: Option<&str>,
) -> Result<Vec<TaskResponse>, ApiError> {
    sqlx::query_as::<_, TaskResponse>(
        "SELECT tasks.id, tasks.space_id, tasks.source_message_id, tasks.channel_id, \
                channels.slug::text AS channel_slug, messages.channel_seq AS source_seq, \
                tasks.title, tasks.status, tasks.created_by_member_id, \
                creators.display_name AS creator_name, tasks.assigned_agent_member_id, \
                assignees.display_name AS assignee_name, tasks.created_at, tasks.updated_at \
         FROM tasks JOIN channels ON channels.id = tasks.channel_id \
         JOIN messages ON messages.id = tasks.source_message_id \
         JOIN members creators ON creators.id = tasks.created_by_member_id \
         LEFT JOIN members assignees ON assignees.id = tasks.assigned_agent_member_id \
         JOIN channel_members visible ON visible.channel_id = tasks.channel_id AND visible.member_id = $2 \
         WHERE tasks.space_id = $1 AND ($3::text IS NULL OR tasks.status = $3) \
         ORDER BY CASE tasks.status WHEN 'in_progress' THEN 0 WHEN 'open' THEN 1 \
                  WHEN 'done' THEN 2 ELSE 3 END, tasks.updated_at DESC",
    )
    .bind(space_id)
    .bind(member_id)
    .bind(status)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)
}

pub(super) async fn convert_for_agent(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    message_id: Uuid,
    title: Option<String>,
    assigned_agent_id: Option<Uuid>,
    key: Uuid,
) -> Result<TaskResponse, ApiError> {
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let source: Option<(Uuid, Uuid, i64, String)> = sqlx::query_as(
        "SELECT messages.space_id, messages.channel_id, messages.channel_seq, messages.body_markdown \
         FROM messages JOIN channel_members ON channel_members.channel_id = messages.channel_id \
         WHERE messages.id = $1 AND channel_members.member_id = $2 \
           AND messages.thread_id IS NULL AND messages.deleted_at IS NULL FOR UPDATE OF messages",
    )
    .bind(message_id)
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, channel_id, _, body) = source.ok_or_else(|| {
        ApiError::validation(
            "invalid_task_source",
            "Task source must be a visible, undeleted Channel root Message",
        )
    })?;
    let title = match title {
        Some(title) => normalize_title(&title)?,
        None => first_line(&body).chars().take(200).collect(),
    };
    validate_assignee(&mut transaction, space_id, channel_id, assigned_agent_id).await?;
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "message_id": message_id, "title": title, "assigned_agent_id": assigned_agent_id,
    }))?;
    let scope = format!("agent:{agent_id}:task:convert");
    if let Some((_status, response)) = idempotency::begin::<TaskResponse>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let response = insert_task(
        &mut transaction,
        space_id,
        channel_id,
        message_id,
        agent_id,
        assigned_agent_id,
        &title,
    )
    .await?;
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(key),
        axum::http::StatusCode::CREATED,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

pub(super) async fn create_for_agent(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
    title: String,
    body: String,
    assigned_agent_id: Option<Uuid>,
    key: Uuid,
) -> Result<TaskResponse, ApiError> {
    let slug = address
        .strip_prefix('#')
        .filter(|value| !value.is_empty() && !value.contains(':'))
        .ok_or_else(|| {
            ApiError::validation(
                "invalid_channel_address",
                "Task create requires a #channel root timeline address",
            )
        })?;
    let title = normalize_title(&title)?;
    let body = body.trim();
    if !(1..=20_000).contains(&body.chars().count()) {
        return Err(ApiError::validation(
            "invalid_message_body",
            "Task Message must contain 1 to 20000 characters",
        ));
    }
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "address": address, "title": title, "body": body, "assigned_agent_id": assigned_agent_id,
    }))?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let channel: Option<(Uuid, Uuid, String, Option<OffsetDateTime>)> = sqlx::query_as(
        "SELECT channels.id, channels.space_id, channels.kind, channels.archived_at \
         FROM channels JOIN channel_members ON channel_members.channel_id = channels.id \
         WHERE channels.slug = $1 AND channel_members.member_id = $2 AND channels.kind <> 'direct' \
         FOR UPDATE OF channels",
    )
    .bind(slug)
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (channel_id, space_id, kind, archived_at) = channel.ok_or_else(|| {
        ApiError::forbidden(
            "permission_denied",
            "Current Agent is not a member of that Channel",
        )
    })?;
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel is read-only",
        ));
    }
    validate_assignee(&mut transaction, space_id, channel_id, assigned_agent_id).await?;
    let scope = format!("agent:{agent_id}:task:create");
    if let Some((_status, response)) = idempotency::begin::<TaskResponse>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let published = super::message_publish::publish(
        &mut transaction,
        super::message_publish::PublishMessage {
            space_id,
            channel_id,
            channel_kind: &kind,
            author_member_id: agent_id,
            body_markdown: body,
            mention_member_ids: &[],
            attachment_ids: &[],
            thread_id: None,
            thread_root_message_id: None,
            reply_to_message_id: None,
            idempotency_key: key,
        },
    )
    .await?;
    let message_id = published.id;
    let response = insert_task(
        &mut transaction,
        space_id,
        channel_id,
        message_id,
        agent_id,
        assigned_agent_id,
        &title,
    )
    .await?;
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(key),
        axum::http::StatusCode::CREATED,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn insert_task(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    channel_id: Uuid,
    message_id: Uuid,
    creator_id: Uuid,
    assignee_id: Option<Uuid>,
    title: &str,
) -> Result<TaskResponse, ApiError> {
    let id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let result = sqlx::query(
        "INSERT INTO tasks (id, space_id, source_message_id, channel_id, created_by_member_id, \
         assigned_agent_member_id, title, status, created_at, updated_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, 'open', $8, $8)",
    )
    .bind(id)
    .bind(space_id)
    .bind(message_id)
    .bind(channel_id)
    .bind(creator_id)
    .bind(assignee_id)
    .bind(title)
    .bind(now)
    .execute(&mut **transaction)
    .await;
    if let Err(error) = result {
        if crate::database::is_unique_constraint(&error, "tasks_source_message_id_key") {
            return Err(ApiError::conflict(
                "task_exists",
                "That Message is already a Task",
            ));
        }
        return Err(ApiError::database(error));
    }
    record_change(transaction, space_id, creator_id, id, "task.created", now).await?;
    task_by_id(transaction, id).await
}

pub(super) async fn claim_for_agent(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    task_id: Uuid,
    key: Uuid,
) -> Result<TaskResponse, ApiError> {
    mutate_task(
        database,
        agent_id,
        task_id,
        Some(agent_id),
        Some("in_progress"),
        "claim",
        key,
    )
    .await
}

pub(super) async fn assign_for_agent(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    task_id: Uuid,
    assignee_id: Uuid,
    key: Uuid,
) -> Result<TaskResponse, ApiError> {
    mutate_task(
        database,
        agent_id,
        task_id,
        Some(assignee_id),
        None,
        "assign",
        key,
    )
    .await
}

pub(super) async fn status_for_agent(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    task_id: Uuid,
    status: String,
    key: Uuid,
) -> Result<TaskResponse, ApiError> {
    validate_status(&status)?;
    mutate_task(
        database,
        agent_id,
        task_id,
        None,
        Some(&status),
        "status",
        key,
    )
    .await
}

async fn mutate_task(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    task_id: Uuid,
    assignee: Option<Uuid>,
    status: Option<&str>,
    action: &str,
    key: Uuid,
) -> Result<TaskResponse, ApiError> {
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "task_id": task_id, "assignee": assignee, "status": status,
    }))?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let target: Option<(Uuid, Uuid, Option<Uuid>)> = sqlx::query_as(
        "SELECT tasks.space_id, tasks.channel_id, tasks.assigned_agent_member_id \
         FROM tasks JOIN channel_members ON channel_members.channel_id = tasks.channel_id \
         WHERE tasks.id = $1 AND channel_members.member_id = $2 FOR UPDATE OF tasks",
    )
    .bind(task_id)
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, channel_id, current_assignee) = target.ok_or_else(|| {
        ApiError::forbidden(
            "permission_denied",
            "Task is not visible to the current Agent",
        )
    })?;
    let final_assignee = assignee.or(current_assignee);
    validate_assignee(&mut transaction, space_id, channel_id, final_assignee).await?;
    if status == Some("in_progress") && final_assignee.is_none() {
        return Err(ApiError::validation(
            "task_assignee_required",
            "in_progress Task requires an assignee",
        ));
    }
    let scope = format!("agent:{agent_id}:task:{task_id}:{action}");
    if let Some((_status, response)) = idempotency::begin::<TaskResponse>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "UPDATE tasks SET assigned_agent_member_id = COALESCE($2, assigned_agent_member_id), \
         status = COALESCE($3, status), updated_at = $4 WHERE id = $1",
    )
    .bind(task_id)
    .bind(assignee)
    .bind(status)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    record_change(
        &mut transaction,
        space_id,
        agent_id,
        task_id,
        "task.updated",
        now,
    )
    .await?;
    let response = task_by_id(&mut transaction, task_id).await?;
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(key),
        axum::http::StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn validate_assignee(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    channel_id: Uuid,
    assignee_id: Option<Uuid>,
) -> Result<(), ApiError> {
    let Some(assignee_id) = assignee_id else {
        return Ok(());
    };
    let valid: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM agents JOIN members ON members.id = agents.member_id \
         JOIN channel_members ON channel_members.member_id = agents.member_id \
         WHERE agents.member_id = $1 AND members.space_id = $2 AND channel_members.channel_id = $3 \
           AND agents.desired_lifecycle = 'active' AND agents.provision_status = 'ready' \
           AND members.retired_at IS NULL)",
    )
    .bind(assignee_id)
    .bind(space_id)
    .bind(channel_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    if !valid {
        return Err(ApiError::validation(
            "invalid_task_assignee",
            "Task assignee must be an active Agent in the source Channel",
        ));
    }
    Ok(())
}

async fn record_change(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    actor_id: Uuid,
    task_id: Uuid,
    action: &'static str,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    audit::record(
        transaction,
        audit::Event {
            space_id,
            actor_id: Some(actor_id),
            action,
            subject_type: "task",
            subject_id: task_id,
            metadata: None,
            occurred_at: now,
        },
    )
    .await?;
    outbox::publish(
        transaction,
        action,
        task_id,
        serde_json::json!({ "space_id": space_id, "task_id": task_id }),
        now,
    )
    .await
}

async fn task_by_id(
    transaction: &mut Transaction<'_, Postgres>,
    task_id: Uuid,
) -> Result<TaskResponse, ApiError> {
    sqlx::query_as::<_, TaskResponse>(
        "SELECT tasks.id, tasks.space_id, tasks.source_message_id, tasks.channel_id, \
                channels.slug::text AS channel_slug, messages.channel_seq AS source_seq, \
                tasks.title, tasks.status, tasks.created_by_member_id, creators.display_name AS creator_name, \
                tasks.assigned_agent_member_id, assignees.display_name AS assignee_name, tasks.created_at, tasks.updated_at \
         FROM tasks JOIN channels ON channels.id = tasks.channel_id JOIN messages ON messages.id = tasks.source_message_id \
         JOIN members creators ON creators.id = tasks.created_by_member_id \
         LEFT JOIN members assignees ON assignees.id = tasks.assigned_agent_member_id WHERE tasks.id = $1",
    ).bind(task_id).fetch_one(&mut **transaction).await.map_err(ApiError::database)
}

fn normalize_title(title: &str) -> Result<String, ApiError> {
    let title = title.trim();
    if !(1..=200).contains(&title.chars().count()) {
        return Err(ApiError::validation(
            "invalid_task_title",
            "Task title must contain 1 to 200 characters",
        ));
    }
    Ok(title.to_owned())
}

fn first_line(body: &str) -> &str {
    body.lines()
        .find(|line| !line.trim().is_empty())
        .map(str::trim)
        .unwrap_or("Task")
}

fn validate_optional_status(status: Option<&str>) -> Result<(), ApiError> {
    if let Some(status) = status {
        validate_status(status)?;
    }
    Ok(())
}

fn validate_status(status: &str) -> Result<(), ApiError> {
    if !STATUSES.contains(&status) {
        return Err(ApiError::validation(
            "invalid_task_status",
            "Task status must be open, in_progress, done, or canceled",
        ));
    }
    Ok(())
}
