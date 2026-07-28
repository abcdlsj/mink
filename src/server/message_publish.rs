use std::collections::HashSet;

use sqlx::{Postgres, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use super::{api_error::ApiError, attachment, outbox};

pub(super) struct PublishMessage<'a> {
    pub space_id: Uuid,
    pub channel_id: Uuid,
    pub channel_kind: &'a str,
    pub author_member_id: Uuid,
    pub body_markdown: &'a str,
    pub mention_member_ids: &'a [Uuid],
    pub attachment_ids: &'a [Uuid],
    pub thread_id: Option<i64>,
    pub thread_root_message_id: Option<Uuid>,
    pub reply_to_message_id: Option<Uuid>,
    pub idempotency_key: Uuid,
}

pub(super) struct PublishedMessage {
    pub id: Uuid,
}

pub(super) async fn publish(
    transaction: &mut Transaction<'_, Postgres>,
    input: PublishMessage<'_>,
) -> Result<PublishedMessage, ApiError> {
    validate_target(transaction, &input).await?;

    let seq: i64 = sqlx::query_scalar(
        "UPDATE channels SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1",
    )
    .bind(input.channel_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let message_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO messages \
         (id, channel_id, space_id, channel_seq, thread_id, reply_to_message_id, \
          author_member_id, body_markdown, idempotency_key, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
    )
    .bind(message_id)
    .bind(input.channel_id)
    .bind(input.space_id)
    .bind(seq)
    .bind(input.thread_id)
    .bind(input.reply_to_message_id)
    .bind(input.author_member_id)
    .bind(input.body_markdown)
    .bind(input.idempotency_key)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;

    attachment::attach_to_message(
        transaction,
        message_id,
        input.space_id,
        input.author_member_id,
        input.attachment_ids,
    )
    .await?;
    insert_mentions(transaction, &input, message_id).await?;

    let inbox_changed = if let Some(thread_id) = input.thread_id {
        subscribe(
            transaction,
            input.channel_id,
            thread_id,
            input.author_member_id,
            now,
        )
        .await?;
        publish_thread_attention(transaction, &input, message_id, seq, thread_id, now).await?
    } else {
        publish_timeline_attention(transaction, &input, message_id, seq, now).await?
    };
    if inbox_changed {
        outbox::publish(
            transaction,
            "inbox.changed",
            message_id,
            serde_json::json!({
                "space_id": input.space_id,
                "channel_id": input.channel_id,
                "thread_id": input.thread_id,
            }),
            now,
        )
        .await?;
    }
    outbox::publish(
        transaction,
        "message.created",
        message_id,
        serde_json::json!({
            "space_id": input.space_id,
            "channel_id": input.channel_id,
            "thread_id": input.thread_id,
            "message_id": message_id,
            "channel_seq": seq,
        }),
        now,
    )
    .await?;

    Ok(PublishedMessage { id: message_id })
}

async fn validate_target(
    transaction: &mut Transaction<'_, Postgres>,
    input: &PublishMessage<'_>,
) -> Result<(), ApiError> {
    if !input.mention_member_ids.is_empty() {
        let valid: HashSet<Uuid> = sqlx::query_scalar(
            "SELECT member_id FROM channel_members WHERE channel_id = $1 AND member_id = ANY($2)",
        )
        .bind(input.channel_id)
        .bind(input.mention_member_ids)
        .fetch_all(&mut **transaction)
        .await
        .map_err(ApiError::database)?
        .into_iter()
        .collect();
        if input
            .mention_member_ids
            .iter()
            .any(|member_id| !valid.contains(member_id))
        {
            return Err(ApiError::validation(
                "invalid_mention",
                "Mentioned Member must belong to the Channel",
            ));
        }
    }

    let Some(thread_id) = input.thread_id else {
        if input.reply_to_message_id.is_some() {
            return Err(ApiError::validation(
                "invalid_reply_target",
                "Main timeline Message cannot reply to a Thread Message",
            ));
        }
        return Ok(());
    };
    if let Some(reply_to_message_id) = input.reply_to_message_id {
        let root_message_id = input.thread_root_message_id.ok_or(ApiError::Internal)?;
        let valid_reply: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1 AND channel_id = $2 \
             AND (id = $3 OR thread_id = $4))",
        )
        .bind(reply_to_message_id)
        .bind(input.channel_id)
        .bind(root_message_id)
        .bind(thread_id)
        .fetch_one(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
        if !valid_reply {
            return Err(ApiError::validation(
                "invalid_reply_target",
                "Reply target must be the Thread root or a Message in this Thread",
            ));
        }
    }
    Ok(())
}

async fn insert_mentions(
    transaction: &mut Transaction<'_, Postgres>,
    input: &PublishMessage<'_>,
    message_id: Uuid,
) -> Result<(), ApiError> {
    for member_id in input.mention_member_ids {
        sqlx::query(
            "INSERT INTO message_mentions (message_id, channel_id, space_id, member_id) \
             VALUES ($1, $2, $3, $4)",
        )
        .bind(message_id)
        .bind(input.channel_id)
        .bind(input.space_id)
        .bind(member_id)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    }
    Ok(())
}

async fn publish_timeline_attention(
    transaction: &mut Transaction<'_, Postgres>,
    input: &PublishMessage<'_>,
    message_id: Uuid,
    seq: i64,
    now: OffsetDateTime,
) -> Result<bool, ApiError> {
    if input.channel_kind == "direct" {
        insert_direct_inbox(transaction, input, message_id, seq, None, now).await?;
        return Ok(true);
    }
    let mut changed = false;
    for member_id in input
        .mention_member_ids
        .iter()
        .filter(|member_id| **member_id != input.author_member_id)
    {
        insert_hard_inbox(
            transaction,
            input,
            message_id,
            seq,
            None,
            *member_id,
            "mention",
            now,
        )
        .await?;
        changed = true;
    }
    let ambient_changed = insert_channel_ambient(transaction, input, message_id, seq, now).await?;
    Ok(changed || ambient_changed)
}

async fn publish_thread_attention(
    transaction: &mut Transaction<'_, Postgres>,
    input: &PublishMessage<'_>,
    message_id: Uuid,
    seq: i64,
    thread_id: i64,
    now: OffsetDateTime,
) -> Result<bool, ApiError> {
    for member_id in input.mention_member_ids {
        subscribe(transaction, input.channel_id, thread_id, *member_id, now).await?;
    }
    if input.channel_kind == "direct" {
        insert_direct_inbox(transaction, input, message_id, seq, Some(thread_id), now).await?;
        return Ok(true);
    }

    let mut hard_recipients = HashSet::new();
    for member_id in input
        .mention_member_ids
        .iter()
        .filter(|member_id| **member_id != input.author_member_id)
    {
        hard_recipients.insert(*member_id);
        insert_hard_inbox(
            transaction,
            input,
            message_id,
            seq,
            Some(thread_id),
            *member_id,
            "mention",
            now,
        )
        .await?;
    }
    if let Some(reply_to_message_id) = input.reply_to_message_id {
        let recipient: Uuid =
            sqlx::query_scalar("SELECT author_member_id FROM messages WHERE id = $1")
                .bind(reply_to_message_id)
                .fetch_one(&mut **transaction)
                .await
                .map_err(ApiError::database)?;
        if recipient != input.author_member_id && hard_recipients.insert(recipient) {
            insert_hard_inbox(
                transaction,
                input,
                message_id,
                seq,
                Some(thread_id),
                recipient,
                "reply",
                now,
            )
            .await?;
        }
    }

    let hard_recipients = hard_recipients.into_iter().collect::<Vec<_>>();
    let recipients: Vec<(Uuid, i64)> = sqlx::query_as(
        "SELECT subscriptions.member_id, \
                COALESCE((agents.attention_config_json->>'ambient_debounce_seconds')::bigint, 5) \
         FROM thread_subscriptions subscriptions \
         LEFT JOIN agents ON agents.member_id = subscriptions.member_id \
         WHERE subscriptions.channel_id = $1 AND subscriptions.thread_id = $2 \
           AND subscriptions.muted_at IS NULL AND subscriptions.member_id <> $3 \
           AND NOT (subscriptions.member_id = ANY($4)) \
           AND (agents.member_id IS NULL OR (agents.desired_lifecycle IN ('active', 'suspended') \
             AND agents.provision_status = 'ready' \
             AND COALESCE((agents.attention_config_json->>'ambient_enabled')::boolean, false)))",
    )
    .bind(input.channel_id)
    .bind(thread_id)
    .bind(input.author_member_id)
    .bind(&hard_recipients)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    for (member_id, debounce_seconds) in &recipients {
        sqlx::query(
            "INSERT INTO inbox_items \
             (id, member_id, space_id, kind, priority, channel_id, thread_id, message_id, \
              first_seq, last_seq, message_count, status, available_at, created_at) \
             VALUES ($1, $2, $3, 'thread_activity', 'ambient', $4, $5, $6, $7, $7, 1, \
                     'pending', $8, $9) \
             ON CONFLICT (member_id, channel_id, thread_id) \
               WHERE kind = 'thread_activity' AND thread_id IS NOT NULL AND status = 'pending' \
             DO UPDATE SET message_id = EXCLUDED.message_id, last_seq = EXCLUDED.last_seq, \
                           message_count = inbox_items.message_count + 1",
        )
        .bind(Uuid::now_v7())
        .bind(member_id)
        .bind(input.space_id)
        .bind(input.channel_id)
        .bind(thread_id)
        .bind(message_id)
        .bind(seq)
        .bind(now + time::Duration::seconds(*debounce_seconds))
        .bind(now)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    }
    Ok(!hard_recipients.is_empty() || !recipients.is_empty())
}

async fn insert_channel_ambient(
    transaction: &mut Transaction<'_, Postgres>,
    input: &PublishMessage<'_>,
    message_id: Uuid,
    seq: i64,
    now: OffsetDateTime,
) -> Result<bool, ApiError> {
    let recipients: Vec<(Uuid, i64)> = sqlx::query_as(
        "SELECT agents.member_id, \
                (agents.attention_config_json->>'ambient_debounce_seconds')::bigint \
         FROM agents JOIN channel_members ON channel_members.member_id = agents.member_id \
         WHERE channel_members.channel_id = $1 AND agents.member_id <> $2 \
           AND agents.desired_lifecycle IN ('active', 'suspended') \
           AND agents.provision_status = 'ready' \
           AND COALESCE((agents.attention_config_json->>'ambient_enabled')::boolean, false) \
           AND NOT (agents.member_id = ANY($3))",
    )
    .bind(input.channel_id)
    .bind(input.author_member_id)
    .bind(input.mention_member_ids)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    for (member_id, debounce_seconds) in &recipients {
        sqlx::query(
            "INSERT INTO inbox_items \
             (id, member_id, space_id, kind, priority, channel_id, message_id, first_seq, \
              last_seq, message_count, status, available_at, created_at) \
             VALUES ($1, $2, $3, 'channel_activity', 'ambient', $4, $5, $6, $6, 1, \
                     'pending', $7, $8) \
             ON CONFLICT (member_id, channel_id) \
               WHERE kind = 'channel_activity' AND thread_id IS NULL AND status = 'pending' \
             DO UPDATE SET message_id = EXCLUDED.message_id, last_seq = EXCLUDED.last_seq, \
                           message_count = inbox_items.message_count + 1",
        )
        .bind(Uuid::now_v7())
        .bind(member_id)
        .bind(input.space_id)
        .bind(input.channel_id)
        .bind(message_id)
        .bind(seq)
        .bind(now + time::Duration::seconds(*debounce_seconds))
        .bind(now)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    }
    Ok(!recipients.is_empty())
}

async fn insert_direct_inbox(
    transaction: &mut Transaction<'_, Postgres>,
    input: &PublishMessage<'_>,
    message_id: Uuid,
    seq: i64,
    thread_id: Option<i64>,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    let recipient: Uuid = sqlx::query_scalar(
        "SELECT member_id FROM channel_members WHERE channel_id = $1 AND member_id <> $2",
    )
    .bind(input.channel_id)
    .bind(input.author_member_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    insert_hard_inbox(
        transaction,
        input,
        message_id,
        seq,
        thread_id,
        recipient,
        "direct",
        now,
    )
    .await
}

#[allow(clippy::too_many_arguments)]
async fn insert_hard_inbox(
    transaction: &mut Transaction<'_, Postgres>,
    input: &PublishMessage<'_>,
    message_id: Uuid,
    seq: i64,
    thread_id: Option<i64>,
    recipient: Uuid,
    kind: &str,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO inbox_items \
         (id, member_id, space_id, kind, priority, channel_id, thread_id, message_id, \
          first_seq, last_seq, available_at, created_at) \
         VALUES ($1, $2, $3, $4, 'hard', $5, $6, $7, $8, $8, $9, $9)",
    )
    .bind(Uuid::now_v7())
    .bind(recipient)
    .bind(input.space_id)
    .bind(kind)
    .bind(input.channel_id)
    .bind(thread_id)
    .bind(message_id)
    .bind(seq)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

async fn subscribe(
    transaction: &mut Transaction<'_, Postgres>,
    channel_id: Uuid,
    thread_id: i64,
    member_id: Uuid,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO thread_subscriptions (channel_id, thread_id, member_id, created_at) \
         VALUES ($1, $2, $3, $4) ON CONFLICT (channel_id, thread_id, member_id) \
         DO UPDATE SET muted_at = NULL",
    )
    .bind(channel_id)
    .bind(thread_id)
    .bind(member_id)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}
