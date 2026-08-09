use super::*;

pub(super) struct MessageWriteContext {
    pub(in crate::server::adapters) idempotency_key: IdempotencyKey,
    pub(in crate::server::adapters) thread_id: Option<Uuid>,
    pub(in crate::server::adapters) handled_item: Option<(Uuid, Uuid)>,
}

pub(super) async fn list_channels(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<ChannelListResponse>, ApiError> {
    let member_id = current_member(&state, &jar, space_id).await?;
    let rows = state
        .read
        .channels_in_space(space_id, member_id)
        .await
        .map_err(application_error)?;
    let mut channels = Vec::with_capacity(rows.len());
    for row in &rows {
        channels.push(channel_row(row, member_id)?);
    }
    Ok(Json(ChannelListResponse {
        channels,
        can_create: true,
    }))
}

pub(super) async fn create_channel(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
    Json(body): Json<CreateChannelBody>,
) -> Result<(StatusCode, Json<ChannelResponse>), ApiError> {
    let member_id = current_member(&state, &jar, space_id).await?;
    let kind = match body.kind.as_str() {
        "public" => ChannelKind::Public,
        "private" => ChannelKind::Private,
        _ => return Err(ApiError::invalid("Channel kind and slug are invalid")),
    };
    let slug = body.slug.trim().to_owned();
    let topic = body
        .topic
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty());
    let channel_id = ChannelId::from_uuid(Uuid::now_v7());
    let now = OffsetDateTime::now_utc();
    let mut audience = BTreeSet::from([MemberId::from_uuid(member_id)]);
    audience.extend(body.agent_member_ids.into_iter().map(MemberId::from_uuid));
    let mut storage = state.storage.clone();
    let channel = CreateChannel::execute(
        &mut storage,
        CreateChannelInput {
            channel_id,
            space_id: SpaceId::from_uuid(space_id),
            audience,
            kind,
            slug: Some(slug.clone()),
            topic: topic.clone(),
            actor_member_id: MemberId::from_uuid(member_id),
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now,
        },
    )
    .await
    .map_err(application_error)?;
    let response_slug = channel.slug.clone().ok_or_else(ApiError::internal)?;
    let response_kind = match channel.kind {
        ChannelKind::Public => ChannelKindCode::Public,
        ChannelKind::Private => ChannelKindCode::Private,
        ChannelKind::Direct => return Err(ApiError::internal()),
    };
    Ok((
        StatusCode::CREATED,
        Json(ChannelResponse {
            id: channel.id.into_uuid(),
            space_id: channel.space_id.into_uuid(),
            slug: response_slug,
            topic: channel.topic,
            kind: response_kind,
            created_by_member_id: member_id,
            joined: true,
            archived_at: channel.archived_at.map(timestamp),
        }),
    ))
}

pub(super) async fn list_channel_members(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelMembersResponse>, ApiError> {
    let viewer_id = channel_member(&state, &jar, channel_id).await?;
    Ok(Json(
        channel_members_response(&state.read, channel_id, viewer_id).await?,
    ))
}

pub(super) async fn channel_members_response(
    queries: &PostgresQueries,
    channel_id: Uuid,
    viewer_id: Uuid,
) -> Result<ChannelMembersResponse, ApiError> {
    let can_manage = queries
        .member_is_governor(viewer_id)
        .await
        .map_err(application_error)?;
    let rows = queries
        .channel_members(channel_id)
        .await
        .map_err(application_error)?;
    let mut members = Vec::with_capacity(rows.len());
    for row in &rows {
        members.push(member_row(queries, row).await?);
    }
    Ok(ChannelMembersResponse {
        members,
        can_manage,
    })
}

pub(super) async fn add_channel_agents(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(channel_id): Path<Uuid>,
    Json(body): Json<AddChannelAgentsBody>,
) -> Result<Json<ChannelMembersResponse>, ApiError> {
    let actor_id = channel_member(&state, &jar, channel_id).await?;
    let key = idempotency_header(&headers)?;
    let agent_ids = body.agent_member_ids.into_iter().collect::<BTreeSet<_>>();
    if agent_ids.is_empty() {
        return Err(ApiError::invalid("At least one Agent is required"));
    }
    let agent_ids = agent_ids.into_iter().collect::<Vec<_>>();
    let mut storage = state.storage.clone();
    AddChannelAgents::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ChannelId::from_uuid(channel_id),
        agent_ids.into_iter().map(MemberId::from_uuid).collect(),
        IdempotencyKey::from_uuid(key),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(
        channel_members_response(&state.read, channel_id, actor_id).await?,
    ))
}

pub(super) async fn remove_channel_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path((channel_id, member_id)): Path<(Uuid, Uuid)>,
) -> Result<Json<ChannelMembersResponse>, ApiError> {
    let actor_id = channel_member(&state, &jar, channel_id).await?;
    let mut storage = state.storage.clone();
    RemoveChannelAgent::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ChannelId::from_uuid(channel_id),
        MemberId::from_uuid(member_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(
        channel_members_response(&state.read, channel_id, actor_id).await?,
    ))
}

pub(super) async fn create_root_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(channel_id): Path<Uuid>,
    Json(body): Json<CreateMessageBody>,
) -> Result<(StatusCode, Json<MessageResponse>), ApiError> {
    let member_id = channel_member(&state, &jar, channel_id).await?;
    if body.body_markdown.trim().is_empty() {
        return Err(ApiError::invalid("Message body is required"));
    }
    let message_id = insert_message(
        &state,
        channel_id,
        member_id,
        MessageWriteContext {
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            thread_id: None,
            handled_item: None,
        },
        body,
    )
    .await
    .map_err(application_error)?;
    let row = state
        .read
        .message(message_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    Ok((
        StatusCode::CREATED,
        Json(message_row(&state.read, &row).await?),
    ))
}

pub(super) async fn read_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
) -> Result<Json<ThreadReadResponse>, ApiError> {
    let thread = state
        .read
        .root_thread(thread_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    let channel_id = thread.channel_id;
    let member_id = channel_member(&state, &jar, channel_id).await?;
    let rows = state
        .read
        .messages_in_thread(thread_id)
        .await
        .map_err(application_error)?;
    let mut projected = Vec::with_capacity(rows.len());
    for row in &rows {
        projected.push(message_row(&state.read, row).await?);
    }
    let root = projected.first().cloned().ok_or_else(ApiError::not_found)?;
    let snapshot = state
        .read
        .channel_snapshot(channel_id)
        .await
        .map_err(application_error)?;
    let is_following = state
        .read
        .thread_following(thread_id, member_id)
        .await
        .map_err(application_error)?;
    let task = message_task_summary(&state.read, thread_id).await?;
    let task_relation = match task.as_ref() {
        Some(summary) => {
            let is_source = state
                .read
                .task_is_source(summary.id, thread_id)
                .await
                .map_err(application_error)?;
            Some(if is_source {
                ThreadRelation::Source
            } else {
                ThreadRelation::Related
            })
        }
        None => None,
    };
    Ok(Json(ThreadReadResponse {
        thread_id,
        channel_id,
        root,
        replies: projected.into_iter().skip(1).collect(),
        snapshot_channel_seq: u64::try_from(snapshot).map_err(|_| ApiError::internal())?,
        is_following,
        task,
        task_relation,
    }))
}

pub(super) async fn create_thread_reply(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(thread_id): Path<Uuid>,
    Json(body): Json<CreateMessageBody>,
) -> Result<(StatusCode, Json<MessageResponse>), ApiError> {
    let row = state
        .read
        .root_thread(thread_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    let channel_id = row.channel_id;
    let member_id = channel_member(&state, &jar, channel_id).await?;
    if body.body_markdown.trim().is_empty() {
        return Err(ApiError::invalid("Message body is required"));
    }
    let message_id = insert_message(
        &state,
        channel_id,
        member_id,
        MessageWriteContext {
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            thread_id: Some(thread_id),
            handled_item: None,
        },
        body,
    )
    .await
    .map_err(application_error)?;
    let row = state
        .read
        .message(message_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    Ok((
        StatusCode::CREATED,
        Json(message_row(&state.read, &row).await?),
    ))
}

pub(super) async fn update_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(message_id): Path<Uuid>,
    Json(body): Json<UpdateMessageBody>,
) -> Result<Json<MessageResponse>, ApiError> {
    let channel_id = state
        .read
        .message(message_id)
        .await
        .map_err(application_error)?
        .map(|row| row.channel_id)
        .ok_or_else(ApiError::not_found)?;
    let actor = channel_member(&state, &jar, channel_id).await?;
    let mut storage = state.storage.clone();
    EditMessage::execute(
        &mut storage,
        EditMessageInput {
            message_id: MessageId::from_uuid(message_id),
            actor_member_id: MemberId::from_uuid(actor),
            body_markdown: body.body_markdown,
            mentions: body.mentions.into_iter().map(MemberId::from_uuid).collect(),
            mention_all: body.mention_all,
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    let row = state
        .read
        .message(message_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    Ok(Json(message_row(&state.read, &row).await?))
}

pub(super) async fn delete_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(message_id): Path<Uuid>,
) -> Result<Json<MessageResponse>, ApiError> {
    let channel_id = state
        .read
        .message(message_id)
        .await
        .map_err(application_error)?
        .map(|row| row.channel_id)
        .ok_or_else(ApiError::not_found)?;
    let actor = channel_member(&state, &jar, channel_id).await?;
    let mut storage = state.storage.clone();
    DeleteMessage::execute(
        &mut storage,
        MessageId::from_uuid(message_id),
        MemberId::from_uuid(actor),
        IdempotencyKey::from_uuid(idempotency_header(&headers)?),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let row = state
        .read
        .message(message_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    Ok(Json(message_row(&state.read, &row).await?))
}

pub(super) async fn insert_message(
    state: &RuntimeState,
    channel_id: Uuid,
    author: Uuid,
    context: MessageWriteContext,
    body: CreateMessageBody,
) -> Result<Uuid, ApplicationError> {
    let message_id = MessageId::from_uuid(Uuid::now_v7());
    let mut storage = state.storage.clone();
    let published = PublishMessage::execute(
        &mut storage,
        MessageDraft {
            message_id,
            channel_id: ChannelId::from_uuid(channel_id),
            author_member_id: MemberId::from_uuid(author),
            idempotency_key: context.idempotency_key,
            thread_id: context.thread_id.map(ThreadId::from_uuid),
            reply_to_message_id: body.reply_to_message_id.map(MessageId::from_uuid),
            body_markdown: body.body_markdown,
            mentions: body.mentions.into_iter().map(MemberId::from_uuid).collect(),
            mention_all: body.mention_all,
            attachment_ids: body
                .attachment_ids
                .into_iter()
                .map(AttachmentId::from_uuid)
                .collect(),
            handled_item: context.handled_item.map(|(run_id, item_id)| {
                (RunId::from_uuid(run_id), InboxItemId::from_uuid(item_id))
            }),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await?;
    for item_id in published.hard_item_ids {
        let mut storage = state.storage.clone();
        if let Err(error) =
            RouteHardItem::execute(&mut storage, RouteHardItemInput { item_id }).await
        {
            tracing::warn!(%item_id, error = %error, "hard Inbox Item remains pending after immediate routing failed");
        }
    }
    Ok(published.message_id.into_uuid())
}

pub(super) async fn channel_member(
    state: &RuntimeState,
    jar: &CookieJar,
    channel_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let member_id = AuthorizeChannelAccess::execute(
        &mut storage,
        &token,
        ChannelId::from_uuid(channel_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(member_id.into_uuid())
}

pub(super) async fn list_direct_messages(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<DirectMessageResponse>>, ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let conversations = ListDirectMessages::execute(
        &mut storage,
        MemberId::from_uuid(member),
        SpaceId::from_uuid(space_id),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(
        conversations.iter().map(direct_message_response).collect(),
    ))
}

/// Returns the redacted Agent-to-Agent DM directory for one governed Agent.
/// Message content remains protected by the channel membership check; this endpoint only exposes
/// the two-Agent conversation metadata needed by the Agent management page.
pub(super) async fn list_agent_direct_messages(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(member_id): Path<Uuid>,
) -> Result<Json<Vec<DirectMessageResponse>>, ApiError> {
    let target = MemberId::from_uuid(member_id);
    let _governor = require_agent_governor(&state, &jar, member_id).await?;
    let space_id = state
        .read
        .agent(member_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?
        .space_id;
    let mut storage = state.storage.clone();
    let conversations =
        ListDirectMessages::execute(&mut storage, target, SpaceId::from_uuid(space_id))
            .await
            .map_err(application_error)?;
    Ok(Json(
        conversations
            .iter()
            .filter(|conversation| conversation.other_member.kind == MemberKind::Agent)
            .map(direct_message_response)
            .collect(),
    ))
}

pub(super) async fn open_direct_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
    Json(body): Json<OpenDirectMessageBody>,
) -> Result<(StatusCode, Json<DirectMessageResponse>), ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let opened = OpenDirectMessage::execute(
        &mut storage,
        OpenDirectMessageInput {
            channel_id: ChannelId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(space_id),
            actor_member_id: MemberId::from_uuid(member),
            other_member_id: MemberId::from_uuid(body.member_id),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        if opened.created {
            StatusCode::CREATED
        } else {
            StatusCode::OK
        },
        Json(direct_message_response(&opened.view)),
    ))
}

pub(super) async fn member_inbox(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(member_id): Path<Uuid>,
    Query(query): Query<InboxQuery>,
) -> Result<Json<Vec<InboxItemResponse>>, ApiError> {
    let scope = match query.status.as_deref() {
        None => InboxScope::Queue,
        Some("dead") => InboxScope::Dead,
        Some(_) => return Err(ApiError::invalid("status accepts only dead")),
    };
    let target = MemberId::from_uuid(member_id);
    let mut storage = state.storage.clone();
    let space_id = storage
        .transact(async |transaction| {
            transaction
                .space_of_member(target)
                .await?
                .ok_or(ApplicationError::NotFound)
        })
        .await
        .map_err(application_error)?;
    let actor = current_member(&state, &jar, space_id.into_uuid()).await?;
    let items = ReadMemberInbox::execute(
        &mut storage,
        MemberId::from_uuid(actor),
        target,
        space_id,
        scope,
    )
    .await
    .map_err(application_error)?;
    Ok(Json(items.iter().map(inbox_item_response).collect()))
}

/// Returns a retired Item to the queue. The Item belongs to an Agent, so the caller is authorized as a
/// governor of that Agent's Space; the Space is resolved from the Item rather than taken from the path.
pub(super) async fn requeue_inbox_item(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(item_id): Path<Uuid>,
) -> Result<Json<InboxItemResponse>, ApiError> {
    let item_id = InboxItemId::from_uuid(item_id);
    let mut storage = state.storage.clone();
    let space_id = state
        .read
        .inbox_space(item_id.into_uuid())
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    let actor = current_member(&state, &jar, space_id).await?;
    let item = RequeueDeadItem::execute(
        &mut storage,
        RequeueDeadItemInput {
            item_id,
            actor_id: MemberId::from_uuid(actor),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(inbox_item_response(&item)))
}

/// Marks the caller's own Human-owned Item handled. The owner opened the source and has seen it, so
/// the Item leaves the queue; an already handled Item is idempotent. Agent Items never resolve
/// through this endpoint, only through the Run that leased them.
pub(super) async fn read_inbox_item(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(item_id): Path<Uuid>,
) -> Result<Json<InboxItemResponse>, ApiError> {
    let item_id = InboxItemId::from_uuid(item_id);
    let mut storage = state.storage.clone();
    let space_id = state
        .read
        .inbox_space(item_id.into_uuid())
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    let actor = current_member(&state, &jar, space_id).await?;
    let item = MarkInboxItemRead::execute(
        &mut storage,
        MarkInboxItemReadInput {
            item_id,
            actor_id: MemberId::from_uuid(actor),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(inbox_item_response(&item)))
}

/// Marks every pending Item of the caller's own Human Inbox handled in one transaction. Agent
/// Inboxes and another Human's Inbox are rejected here, matching single-Item read authorization.
pub(super) async fn read_all_inbox_items(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(member_id): Path<Uuid>,
) -> Result<Json<MarkAllInboxReadResponse>, ApiError> {
    let target = MemberId::from_uuid(member_id);
    let mut storage = state.storage.clone();
    let space_id = storage
        .transact(async |transaction| {
            transaction
                .space_of_member(target)
                .await?
                .ok_or(ApplicationError::NotFound)
        })
        .await
        .map_err(application_error)?;
    let actor = current_member(&state, &jar, space_id.into_uuid()).await?;
    let count = MarkAllInboxRead::execute(
        &mut storage,
        MarkAllInboxReadInput {
            actor_id: MemberId::from_uuid(actor),
            target_id: target,
            space_id,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(MarkAllInboxReadResponse { count }))
}

pub(super) fn direct_message_response(conversation: &DirectMessageView) -> DirectMessageResponse {
    DirectMessageResponse {
        channel_id: conversation.channel_id.into_uuid(),
        space_id: conversation.space_id.into_uuid(),
        other_member: space_member_response(&conversation.other_member),
        created_at: timestamp(conversation.created_at),
    }
}

pub(super) fn space_member_response(member: &SpaceMemberView) -> MemberResponse {
    MemberResponse {
        id: member.id.into_uuid(),
        kind: match member.kind {
            MemberKind::Human => MemberKindCode::Human,
            MemberKind::Agent => MemberKindCode::Agent,
        },
        display_name: member.display_name.clone(),
        access_level: access_level_code(member.access_level),
        permissions: member
            .permissions
            .iter()
            .map(|action| action.code().to_owned())
            .collect(),
    }
}

pub(super) fn inbox_item_response(item: &InboxItemView) -> InboxItemResponse {
    InboxItemResponse {
        id: item.id.into_uuid(),
        space_id: item.space_id.into_uuid(),
        member_id: item.member_id.into_uuid(),
        kind: inbox_kind_code(item.kind),
        priority: match item.strength {
            AttentionStrength::Hard => InboxPriority::Hard,
            AttentionStrength::Ambient => InboxPriority::Ambient,
        },
        channel_id: item.channel_id.map(ChannelId::into_uuid),
        channel_slug: item.channel_slug.clone(),
        thread_id: item.thread_id.map(ThreadId::into_uuid),
        message_id: item.message_id.map(MessageId::into_uuid),
        sender_member_id: item.sender_member_id.map(MemberId::into_uuid),
        sender_display_name: item.sender_display_name.clone(),
        message_preview: item.message_preview.clone(),
        activity_events: item
            .activity_events
            .iter()
            .map(|event| InboxActivityEventResponse {
                sequence: event.sequence,
                kind: match event.kind {
                    InboxActivityEventKindView::Message => InboxActivityEventKind::Message,
                    InboxActivityEventKindView::MemberJoined => {
                        InboxActivityEventKind::MemberJoined
                    }
                    InboxActivityEventKindView::MemberLeft => InboxActivityEventKind::MemberLeft,
                },
                message_id: event.message_id.map(MessageId::into_uuid),
                member_id: event.member_id.map(MemberId::into_uuid),
            })
            .collect(),
        summary: inbox_summary(item).to_owned(),
        status: inbox_status_code(item.status),
        available_at: timestamp(item.available_at),
        created_at: timestamp(item.created_at),
        retry_count: item.retry_count,
        requeue_count: item.requeue_count,
    }
}

pub(super) fn inbox_summary(item: &InboxItemView) -> &'static str {
    match item.kind {
        InboxItemKind::Direct => "Direct message",
        InboxItemKind::Mention => "You were mentioned",
        InboxItemKind::Reply => "Reply to your Message",
        InboxItemKind::TaskActivity => "Linked Thread activity",
        InboxItemKind::ThreadActivity => "Thread activity",
        InboxItemKind::ChannelActivity => "Channel activity",
        InboxItemKind::System => "System notice",
    }
}

pub(super) fn inbox_kind_code(kind: InboxItemKind) -> InboxKind {
    match kind {
        InboxItemKind::Direct => InboxKind::Direct,
        InboxItemKind::Mention => InboxKind::Mention,
        InboxItemKind::Reply => InboxKind::Reply,
        InboxItemKind::TaskActivity => InboxKind::TaskActivity,
        InboxItemKind::ThreadActivity => InboxKind::ThreadActivity,
        InboxItemKind::ChannelActivity => InboxKind::ChannelActivity,
        InboxItemKind::System => InboxKind::System,
    }
}

pub(super) fn inbox_status_code(status: InboxItemStatus) -> InboxStatus {
    match status {
        InboxItemStatus::Pending => InboxStatus::Pending,
        InboxItemStatus::Assigned => InboxStatus::Assigned,
        InboxItemStatus::Deferred => InboxStatus::Deferred,
        InboxItemStatus::Handled => InboxStatus::Handled,
        InboxItemStatus::Dead => InboxStatus::Dead,
    }
}

pub(super) fn access_level_code(level: AccessLevel) -> AccessLevelCode {
    match level {
        AccessLevel::Owner => AccessLevelCode::Owner,
        AccessLevel::Admin => AccessLevelCode::Admin,
        AccessLevel::Member => AccessLevelCode::Member,
    }
}

pub(super) async fn join_channel(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelResponse>, ApiError> {
    let space_id = space_of_channel(&state, channel_id).await?;
    let actor_id = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    JoinChannel::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ChannelId::from_uuid(channel_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    read_channel_projection(&state, channel_id, actor_id).await
}

pub(super) async fn archive_channel(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelResponse>, ApiError> {
    let space_id = space_of_channel(&state, channel_id).await?;
    let actor_id = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    ArchiveChannel::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ChannelId::from_uuid(channel_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    read_channel_projection(&state, channel_id, actor_id).await
}

pub(super) async fn space_of_channel(
    state: &RuntimeState,
    channel_id: Uuid,
) -> Result<Uuid, ApiError> {
    state
        .read
        .channel_space(channel_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)
}

pub(super) async fn read_channel_projection(
    state: &RuntimeState,
    channel_id: Uuid,
    viewer: Uuid,
) -> Result<Json<ChannelResponse>, ApiError> {
    let row = state
        .read
        .channel(channel_id, viewer)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    Ok(Json(channel_row(&row, viewer)?))
}

pub(super) async fn follow_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    set_subscription(state, jar, thread_id, true).await
}

pub(super) async fn unfollow_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    set_subscription(state, jar, thread_id, false).await
}

pub(super) async fn set_subscription(
    state: RuntimeState,
    jar: CookieJar,
    thread_id: Uuid,
    following: bool,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    let row = state
        .read
        .root_thread(thread_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::not_found)?;
    let actor_id = current_member(&state, &jar, row.space_id).await?;
    let mut storage = state.storage.clone();
    let is_following = SetThreadSubscription::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ThreadId::from_uuid(thread_id),
        following,
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(ThreadSubscriptionResponse {
        thread_id,
        channel_id: row.channel_id,
        is_following,
    }))
}

pub(super) async fn list_messages(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<MessagePageResponse>, ApiError> {
    channel_member(&state, &jar, channel_id).await?;
    let rows = state
        .read
        .root_messages_in_channel(channel_id)
        .await
        .map_err(application_error)?;
    let mut messages = Vec::with_capacity(rows.len());
    for row in rows {
        messages.push(message_row(&state.read, &row).await?);
    }
    let snapshot = state
        .read
        .channel_snapshot(channel_id)
        .await
        .map_err(application_error)?;
    Ok(Json(MessagePageResponse {
        channel_id,
        messages,
        snapshot_channel_seq: u64::try_from(snapshot).map_err(|_| ApiError::internal())?,
        has_more_before: false,
        has_more_after: false,
    }))
}
