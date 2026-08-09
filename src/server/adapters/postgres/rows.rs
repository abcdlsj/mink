use super::*;

pub(super) fn message_from_row(row: &sqlx::postgres::PgRow) -> Result<Message, ApplicationError> {
    let content = match row.get::<&str, _>("content_kind") {
        "text" => MessageContent::Text(row.get("body_markdown")),
        "channel_created" => {
            MessageContent::ChannelCreated(ChannelId::from_uuid(row.get("action_channel_id")))
        }
        "agent_created" => {
            MessageContent::AgentCreated(MemberId::from_uuid(row.get("action_agent_member_id")))
        }
        "system_notice" => MessageContent::SystemNotice(row.get("body_markdown")),
        _ => return Err(ApplicationError::Internal),
    };
    Ok(Message {
        id: MessageId::from_uuid(row.get("id")),
        thread_id: ThreadId::from_uuid(row.get("thread_id")),
        author_member_id: MemberId::from_uuid(row.get("author_member_id")),
        placement: placement_from_str(row.get("placement"))?,
        content,
        created_at: row.get("created_at"),
        edited_at: row.get("edited_at"),
        deleted_at: row.get("deleted_at"),
    })
}

pub(super) fn task_from_row(
    row: &sqlx::postgres::PgRow,
    related_threads: Vec<RelatedThreadSnapshot>,
) -> Result<Task, ApplicationError> {
    Task::rehydrate(DomainTaskSnapshot {
        id: TaskId::from_uuid(row.get("id")),
        seq: u64::try_from(row.get::<i64, _>("seq")).map_err(|_| ApplicationError::Internal)?,
        space_id: SpaceId::from_uuid(row.get("space_id")),
        title: row.get("title"),
        status: task_status_from_str(row.get("status"))?,
        source_thread_id: ThreadId::from_uuid(row.get("source_thread_id")),
        creator_member_id: MemberId::from_uuid(row.get("creator_member_id")),
        assignee_agent_member_id: row
            .get::<Option<Uuid>, _>("assignee_agent_member_id")
            .map(MemberId::from_uuid),
        result_message_id: row
            .get::<Option<Uuid>, _>("result_message_id")
            .map(MessageId::from_uuid),
        close_reason: row
            .get::<Option<String>, _>("close_reason_code")
            .map(|value| close_reason_from_str(&value))
            .transpose()?,
        close_reason_note: row.get("close_reason_note"),
        related_threads,
        created_at: row.get("created_at"),
        updated_at: row.get("updated_at"),
        finished_at: row.get("finished_at"),
    })
    .map_err(Into::into)
}

pub(super) fn run_from_row(
    row: &sqlx::postgres::PgRow,
    items: Vec<RunItemSnapshot>,
) -> Result<Run, ApplicationError> {
    Run::rehydrate(DomainRunSnapshot {
        id: RunId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        agent_id: MemberId::from_uuid(row.get("agent_id")),
        task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
        focus_thread_id: ThreadId::from_uuid(row.get("focus_thread_id")),
        status: run_status_from_str(row.get("status"))?,
        trigger: run_trigger_from_str(row.get("trigger_kind"))?,
        cancel_requested: row.get("cancel_requested"),
        items,
        outcome: row
            .get::<Option<String>, _>("outcome_code")
            .map(|value| run_outcome_from_str(&value))
            .transpose()?,
        error_code: row
            .get::<Option<String>, _>("error_code")
            .map(|value| run_error_code_from_str(&value))
            .transpose()?,
        continuation_note: row.get("continuation_note"),
        started_at: row.get("started_at"),
        finished_at: row.get("finished_at"),
    })
    .map_err(Into::into)
}

pub(super) fn inbox_from_row(row: &sqlx::postgres::PgRow) -> Result<InboxItem, ApplicationError> {
    InboxItem::rehydrate(DomainInboxItemSnapshot {
        id: InboxItemId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        member_id: MemberId::from_uuid(row.get("member_id")),
        message_id: row
            .get::<Option<Uuid>, _>("message_id")
            .map(MessageId::from_uuid),
        thread_id: ThreadId::from_uuid(row.get("thread_id")),
        task_id: row.get::<Option<Uuid>, _>("task_id").map(TaskId::from_uuid),
        kind: inbox_kind_from_str(row.get("kind"))?,
        strength: strength_from_str(row.get("strength"))?,
        status: inbox_status_from_str(row.get("status"))?,
        available_at: row.get("available_at"),
        assigned_run_id: row
            .get::<Option<Uuid>, _>("assigned_run_id")
            .map(RunId::from_uuid),
        retry_count: u32::try_from(row.get::<i32, _>("retry_count"))
            .map_err(|_| ApplicationError::Internal)?,
        requeue_count: u32::try_from(row.get::<i32, _>("requeue_count"))
            .map_err(|_| ApplicationError::Internal)?,
        handled_at: row.get("handled_at"),
        ambient: ambient_from_row(row)?,
    })
    .map_err(Into::into)
}

/// The four aggregate columns are constrained to be present or absent together, so the first one
/// decides whether this Item aggregates.
pub(super) fn ambient_from_row(
    row: &sqlx::postgres::PgRow,
) -> Result<Option<AmbientAggregateSnapshot>, ApplicationError> {
    let Some(first_message_seq) = row.get::<Option<i64>, _>("first_message_seq") else {
        return Ok(None);
    };
    Ok(Some(AmbientAggregateSnapshot {
        first_message_seq: u64::try_from(first_message_seq)
            .map_err(|_| ApplicationError::Internal)?,
        last_message_seq: u64::try_from(row.get::<i64, _>("last_message_seq"))
            .map_err(|_| ApplicationError::Internal)?,
        aggregated_count: u32::try_from(row.get::<i32, _>("aggregated_count"))
            .map_err(|_| ApplicationError::Internal)?,
        force_at: row.get("force_at"),
    }))
}

pub(super) fn wire_message(
    row: &sqlx::postgres::PgRow,
) -> Result<MessageSnapshot, ApplicationError> {
    let content = match row.get::<&str, _>("content_kind") {
        "text" => WireMessageContent::Text {
            markdown: row.get("body_markdown"),
        },
        "channel_created" => WireMessageContent::Action {
            action: ActionKind::ChannelCreated,
            target: ActionTarget::Channel(ChannelId::from_uuid(row.get("action_channel_id"))),
        },
        "agent_created" => WireMessageContent::Action {
            action: ActionKind::AgentCreated,
            target: ActionTarget::Agent(AgentId::from_uuid(row.get("action_agent_member_id"))),
        },
        "system_notice" => WireMessageContent::Text {
            markdown: row.get("body_markdown"),
        },
        _ => return Err(ApplicationError::Internal),
    };
    Ok(MessageSnapshot {
        message_id: MessageId::from_uuid(row.get("id")),
        author_member_id: MemberId::from_uuid(row.get("author_member_id")),
        sequence: u64::try_from(row.get::<i64, _>("channel_seq"))
            .map_err(|_| ApplicationError::Internal)?,
        content,
        created_at: row.get("created_at"),
    })
}

pub(super) fn wire_task_status(value: &str) -> Result<WireTaskStatus, ApplicationError> {
    match value {
        "todo" => Ok(WireTaskStatus::Todo),
        "in_progress" => Ok(WireTaskStatus::InProgress),
        "in_review" => Ok(WireTaskStatus::InReview),
        "done" => Ok(WireTaskStatus::Done),
        "closed" => Ok(WireTaskStatus::Closed),
        _ => Err(ApplicationError::Internal),
    }
}

pub(super) fn wire_inbox_kind(value: &str) -> Result<InboxSourceKind, ApplicationError> {
    match value {
        "direct" => Ok(InboxSourceKind::Direct),
        "mention" => Ok(InboxSourceKind::Mention),
        "reply" => Ok(InboxSourceKind::Reply),
        "task_activity" => Ok(InboxSourceKind::TaskActivity),
        "thread_activity" => Ok(InboxSourceKind::ThreadActivity),
        "channel_activity" => Ok(InboxSourceKind::ChannelActivity),
        "system" => Ok(InboxSourceKind::System),
        _ => Err(ApplicationError::Internal),
    }
}

pub(super) fn wire_activity_event_kind(value: &str) -> Result<ActivityEventKind, ApplicationError> {
    match value {
        "message" => Ok(ActivityEventKind::Message),
        "member_joined" => Ok(ActivityEventKind::MemberJoined),
        "member_left" => Ok(ActivityEventKind::MemberLeft),
        _ => Err(ApplicationError::Internal),
    }
}

pub(super) fn activity_event_kind_str(value: ActivityEventKind) -> &'static str {
    match value {
        ActivityEventKind::Message => "message",
        ActivityEventKind::MemberJoined => "member_joined",
        ActivityEventKind::MemberLeft => "member_left",
    }
}

pub(super) fn wire_strength(value: &str) -> Result<WireAttentionStrength, ApplicationError> {
    match value {
        "hard" => Ok(WireAttentionStrength::Hard),
        "ambient" => Ok(WireAttentionStrength::Ambient),
        _ => Err(ApplicationError::Internal),
    }
}

macro_rules! text_enum {
    ($to:ident, $from:ident, $ty:ty, {$($variant:path => $text:literal),+ $(,)?}) => {
        pub(super) fn $to(value: $ty) -> &'static str { match value { $($variant => $text),+ } }
        pub(super) fn $from(value: &str) -> Result<$ty, ApplicationError> { match value { $($text => Ok($variant)),+, _ => Err(ApplicationError::Internal) } }
    };
}

text_enum!(task_status_str, task_status_from_str, TaskStatus, {
    TaskStatus::Todo => "todo", TaskStatus::InProgress => "in_progress", TaskStatus::InReview => "in_review", TaskStatus::Done => "done", TaskStatus::Closed => "closed"
});
text_enum!(close_reason_str, close_reason_from_str, CloseReason, {
    CloseReason::Invalid => "invalid", CloseReason::Duplicate => "duplicate", CloseReason::NotNeeded => "not_needed", CloseReason::Obsolete => "obsolete", CloseReason::Other => "other"
});
text_enum!(run_status_str, run_status_from_str, RunStatus, {
    RunStatus::Dispatched => "dispatched", RunStatus::Working => "working", RunStatus::Completed => "completed", RunStatus::Yielded => "yielded", RunStatus::Failed => "failed", RunStatus::Canceled => "canceled"
});
text_enum!(run_trigger_str, run_trigger_from_str, RunTrigger, {
    RunTrigger::Mention => "mention", RunTrigger::DirectMessage => "direct_message", RunTrigger::TaskActivity => "task_activity", RunTrigger::ThreadActivity => "thread_activity", RunTrigger::ChannelActivity => "channel_activity", RunTrigger::Schedule => "schedule"
});
text_enum!(run_outcome_str, run_outcome_from_str, RunOutcome, {
    RunOutcome::Completed => "completed", RunOutcome::Yielded => "yielded", RunOutcome::Failed => "failed", RunOutcome::Canceled => "canceled"
});
text_enum!(run_error_code_str, run_error_code_from_str, RunErrorCode, {
    RunErrorCode::DriverError => "driver_error", RunErrorCode::DriverLost => "driver_lost", RunErrorCode::ComputerRestarted => "computer_restarted", RunErrorCode::SessionUnavailable => "session_unavailable", RunErrorCode::AgentUnavailable => "agent_unavailable", RunErrorCode::InvalidCommand => "invalid_command", RunErrorCode::UnhandledItems => "unhandled_items", RunErrorCode::Internal => "internal"
});
text_enum!(delivery_outcome_str, delivery_outcome_from_str, DeliveryOutcome, {
    DeliveryOutcome::Accepted => "accepted", DeliveryOutcome::TooLate => "too_late", DeliveryOutcome::Unsupported => "unsupported"
});
text_enum!(disposition_str, disposition_from_str, InboxItemDisposition, {
    InboxItemDisposition::Handled => "handled", InboxItemDisposition::Deferred => "deferred", InboxItemDisposition::Released => "released"
});
text_enum!(inbox_status_str, inbox_status_from_str, InboxItemStatus, {
    InboxItemStatus::Pending => "pending", InboxItemStatus::Assigned => "assigned", InboxItemStatus::Deferred => "deferred", InboxItemStatus::Handled => "handled", InboxItemStatus::Dead => "dead"
});
text_enum!(inbox_kind_str, inbox_kind_from_str, InboxItemKind, {
    InboxItemKind::Direct => "direct", InboxItemKind::Mention => "mention", InboxItemKind::Reply => "reply", InboxItemKind::TaskActivity => "task_activity", InboxItemKind::ThreadActivity => "thread_activity", InboxItemKind::ChannelActivity => "channel_activity", InboxItemKind::System => "system"
});
pub(super) fn strength_from_str(value: &str) -> Result<AttentionStrength, ApplicationError> {
    match value {
        "hard" => Ok(AttentionStrength::Hard),
        "ambient" => Ok(AttentionStrength::Ambient),
        _ => Err(ApplicationError::Internal),
    }
}
text_enum!(placement_str, placement_from_str, MessagePlacement, {
    MessagePlacement::Root => "root", MessagePlacement::Reply => "reply"
});
pub(super) fn channel_kind_str(value: ChannelKind) -> &'static str {
    match value {
        ChannelKind::Public => "public",
        ChannelKind::Private => "private",
        ChannelKind::Direct => "direct",
    }
}

pub(super) fn channel_kind_from_str(value: &str) -> Result<ChannelKind, ApplicationError> {
    match value {
        "public" => Ok(ChannelKind::Public),
        "private" => Ok(ChannelKind::Private),
        "direct" => Ok(ChannelKind::Direct),
        _ => Err(ApplicationError::Internal),
    }
}
text_enum!(agent_lifecycle_str, agent_lifecycle_from_str, AgentLifecycle, {
    AgentLifecycle::Provisioning => "provisioning", AgentLifecycle::Active => "active", AgentLifecycle::Suspended => "suspended", AgentLifecycle::Retired => "retired", AgentLifecycle::Error => "error"
});
text_enum!(driver_kind_str, driver_kind_from_str, DriverKind, {
    DriverKind::Codex => "codex", DriverKind::Builtin => "builtin"
});

pub(super) fn permission_str(value: PermissionAction) -> &'static str {
    value.code()
}

pub(super) fn permission_from_str(value: &str) -> Result<PermissionAction, ApplicationError> {
    match value {
        "channel.create" => Ok(PermissionAction::ChannelCreate),
        "channel.invite" => Ok(PermissionAction::ChannelInvite),
        "channel.remove" => Ok(PermissionAction::ChannelRemove),
        "agent.create" => Ok(PermissionAction::AgentCreate),
        _ => Err(ApplicationError::Internal),
    }
}

pub(super) fn access_level_str(value: AccessLevel) -> &'static str {
    match value {
        AccessLevel::Owner => "owner",
        AccessLevel::Admin => "admin",
        AccessLevel::Member => "member",
    }
}

pub(super) fn attachment_from_row(
    row: &sqlx::postgres::PgRow,
) -> Result<Attachment, ApplicationError> {
    let length = row
        .get::<Option<i64>, _>("length")
        .map(u64::try_from)
        .transpose()
        .map_err(|_| ApplicationError::Internal)?;
    let sha256 = row
        .get::<Option<Vec<u8>>, _>("sha256")
        .map(<[u8; 32]>::try_from)
        .transpose()
        .map_err(|_| ApplicationError::Internal)?;
    Attachment::rehydrate(AttachmentSnapshot {
        id: AttachmentId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        uploader_member_id: MemberId::from_uuid(row.get("uploader_member_id")),
        name: row.get("name"),
        media_type: row.get("media_type"),
        object_key: row.get("object_key"),
        status: AttachmentStatus::parse(row.get("status"))?,
        length,
        sha256,
        created_at: row.get("created_at"),
        ready_at: row.get("ready_at"),
        deleted_at: row.get("deleted_at"),
    })
    .map_err(Into::into)
}

pub(super) fn paired_computer_from_row(
    row: &sqlx::postgres::PgRow,
) -> Result<PairedComputer, ApplicationError> {
    Ok(PairedComputer {
        id: ComputerId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        name: row.get("name"),
        hostname: row.get("hostname"),
        os: ComputerOs::parse(row.get("os"))?,
        daemon_version: row.get("daemon_version"),
        connected: row.get::<String, _>("connection_status") == "online",
        deleted: row.get::<Option<OffsetDateTime>, _>("deleted_at").is_some(),
        last_seen_at: row.get("last_seen_at"),
        created_at: row.get("created_at"),
    })
}

pub(super) fn member_kind_from_str(value: &str) -> Result<MemberKind, ApplicationError> {
    match value {
        "human" => Ok(MemberKind::Human),
        "agent" => Ok(MemberKind::Agent),
        _ => Err(ApplicationError::Internal),
    }
}

pub(super) fn space_member_from_row(
    row: &sqlx::postgres::PgRow,
    space_id: SpaceId,
    permissions: Vec<PermissionAction>,
) -> Result<SpaceMemberView, ApplicationError> {
    Ok(SpaceMemberView {
        id: MemberId::from_uuid(row.get("id")),
        space_id,
        kind: member_kind_from_str(row.get("kind"))?,
        display_name: row.get("display_name"),
        access_level: access_level_from_str(row.get("access_level"))?,
        permissions,
    })
}

pub(super) fn direct_message_from_row(
    row: &sqlx::postgres::PgRow,
    space_id: SpaceId,
    permissions: Vec<PermissionAction>,
) -> Result<DirectMessageView, ApplicationError> {
    Ok(DirectMessageView {
        channel_id: ChannelId::from_uuid(row.get("channel_id")),
        space_id,
        other_member: space_member_from_row(row, space_id, permissions)?,
        created_at: row.get("created_at"),
    })
}

pub(super) fn inbox_view_from_row(
    row: &sqlx::postgres::PgRow,
) -> Result<InboxItemView, ApplicationError> {
    Ok(InboxItemView {
        id: InboxItemId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        member_id: MemberId::from_uuid(row.get("member_id")),
        kind: inbox_kind_from_str(row.get("kind"))?,
        strength: strength_from_str(row.get("strength"))?,
        status: inbox_status_from_str(row.get("status"))?,
        channel_id: Some(ChannelId::from_uuid(row.get("channel_id"))),
        channel_slug: row.get("channel_slug"),
        thread_id: Some(ThreadId::from_uuid(row.get("thread_id"))),
        message_id: row
            .get::<Option<Uuid>, _>("message_id")
            .map(MessageId::from_uuid),
        sender_member_id: row
            .get::<Option<Uuid>, _>("sender_member_id")
            .map(MemberId::from_uuid),
        sender_display_name: row.get("sender_name"),
        message_preview: row.get("message_preview"),
        activity_events: Vec::new(),
        available_at: row.get("available_at"),
        created_at: row.get("created_at"),
        retry_count: u32::try_from(row.get::<i32, _>("retry_count"))
            .map_err(|_| ApplicationError::Internal)?,
        requeue_count: u32::try_from(row.get::<i32, _>("requeue_count"))
            .map_err(|_| ApplicationError::Internal)?,
    })
}

pub(super) fn invitation_from_row(
    row: &sqlx::postgres::PgRow,
) -> Result<(Uuid, Invitation), ApplicationError> {
    let invitation = Invitation {
        draft: InvitationDraft {
            space_id: SpaceId::from_uuid(row.get("space_id")),
            email_normalized: row.get("email_normalized"),
            token_hash: row.get("token_hash"),
            created_by_member_id: MemberId::from_uuid(row.get("created_by_member_id")),
        },
        status: InvitationStatus::parse(row.get("status"))?,
        expires_at: row.get("expires_at"),
        accepted_by_member_id: row
            .get::<Option<Uuid>, _>("accepted_by_member_id")
            .map(MemberId::from_uuid),
        accepted_at: row.get("accepted_at"),
    };
    Ok((row.get("id"), invitation))
}

pub(super) fn pairing_from_row(row: &sqlx::postgres::PgRow) -> Result<Pairing, ApplicationError> {
    Ok(Pairing {
        request: PairingRequest {
            token_hash: row.get("token_hash"),
            hostname: row.get("hostname"),
            os: ComputerOs::parse(row.get("os"))?,
            daemon_version: row.get("daemon_version"),
        },
        status: PairingStatus::parse(row.get("status"))?,
        expires_at: row.get("expires_at"),
        computer_id: row
            .get::<Option<Uuid>, _>("computer_id")
            .map(ComputerId::from_uuid),
        space_id: row
            .get::<Option<Uuid>, _>("space_id")
            .map(SpaceId::from_uuid),
    })
}

pub(super) fn access_level_from_str(value: &str) -> Result<AccessLevel, ApplicationError> {
    match value {
        "owner" => Ok(AccessLevel::Owner),
        "admin" => Ok(AccessLevel::Admin),
        "member" => Ok(AccessLevel::Member),
        _ => Err(ApplicationError::Internal),
    }
}
