use std::collections::HashSet;

use sha2::{Digest, Sha256};
use time::OffsetDateTime;

use crate::{
    computer::application::{
        ActivityEventInput, AgentInput, ApplicationError, AttentionNoticeInput,
        ChannelActivityInput, ChannelMemberInput, ContextMessageInput, ContinuityState,
        DeliveryState, DispatchedItemInput, DriverKind, ItemDisposition, LocalAgent,
        LocalAgentState, LocalRun, LocalRunState, MemoryEntryInput, MemoryFile, NewRun,
        NoticeLocationInput, RunContextInput, RunInput, RunPriority, SessionFingerprint,
        SessionScope, TaskInput, TerminalStatus, WorkInput, WorkStrength,
        command::{Command as ApplicationCommand, CommandService},
        ports::{
            AgentHomePort, CommandStatus, ComputerTransaction, DriverPort, LlmUsageStore,
            LocalErrorCode, LocalEvent, TransactionPort,
        },
        query::QueryService,
    },
    ids::{AgentId, ChannelId, RunId, ThreadId},
    protocol::computer as wire,
};

pub(in crate::computer) struct ServerConnectionAdapter {
    product_contract: String,
}

impl ServerConnectionAdapter {
    pub(in crate::computer) fn new(product_contract: String) -> Self {
        Self { product_contract }
    }

    #[cfg(test)]
    pub(in crate::computer) async fn receive<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        &self,
        store: &mut P,
        driver: &mut D,
        homes: &mut H,
        envelope: wire::CommandEnvelope,
    ) -> Result<Vec<wire::ComputerFrame>, ApplicationError> {
        self.receive_with_activity(store, driver, homes, envelope, None)
            .await
    }

    pub(in crate::computer) async fn receive_with_activity<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        &self,
        store: &mut P,
        driver: &mut D,
        homes: &mut H,
        envelope: wire::CommandEnvelope,
        channel_activity: Option<wire::ChannelActivitySnapshot>,
    ) -> Result<Vec<wire::ComputerFrame>, ApplicationError> {
        let diagnostic = envelope.command.diagnostic();
        tracing::debug!(
            command_id = %envelope.command_id.into_uuid(),
            sequence = envelope.sequence.0,
            command_kind = diagnostic.kind,
            agent_id = ?diagnostic.agent_id,
            run_id = ?diagnostic.run_id,
            task_id = ?diagnostic.task_id,
            delivery_sequence = diagnostic.delivery_sequence,
            "Computer command received"
        );
        let command = self
            .application_command(store, homes, envelope.command, channel_activity)
            .await?;
        let sequence = envelope.sequence.0;
        let execution =
            CommandService::execute(store, driver, homes, envelope.command_id, sequence, command)
                .await?;
        if execution.status == CommandStatus::Rejected {
            tracing::warn!(
                command_id = %envelope.command_id.into_uuid(),
                sequence,
                command_kind = diagnostic.kind,
                agent_id = ?diagnostic.agent_id,
                run_id = ?diagnostic.run_id,
                task_id = ?diagnostic.task_id,
                delivery_sequence = diagnostic.delivery_sequence,
                error_code = ?computer_error(
                    execution.error.as_ref().unwrap_or(&ApplicationError::Internal)
                ),
                "Computer rejected server command"
            );
        }
        Ok(vec![
            wire::ComputerFrame::CommandAck {
                ack: wire::CommandAck {
                    command_id: envelope.command_id,
                    sequence: envelope.sequence,
                },
            },
            wire::ComputerFrame::CommandResult {
                result: wire::CommandResult {
                    command_id: envelope.command_id,
                    sequence: envelope.sequence,
                    outcome: match execution.status {
                        CommandStatus::Applied => wire::CommandOutcome::Applied,
                        CommandStatus::Rejected => wire::CommandOutcome::Rejected {
                            code: computer_error(
                                execution
                                    .error
                                    .as_ref()
                                    .unwrap_or(&ApplicationError::Internal),
                            ),
                        },
                        CommandStatus::Pending => return Err(ApplicationError::DriverUnavailable),
                    },
                },
            },
        ])
    }

    pub(in crate::computer) async fn answer_query<
        P: TransactionPort + LlmUsageStore,
        H: AgentHomePort,
    >(
        store: &mut P,
        homes: &mut H,
        envelope: wire::QueryEnvelope,
    ) -> wire::ComputerFrame {
        let result = match envelope.query {
            wire::Query::SessionContinuity(query) => {
                match QueryService::session_continuity(
                    store,
                    query.agent_id,
                    local_scope(query.scope),
                )
                .await
                {
                    Ok(continuity) => {
                        wire::QueryResult::SessionContinuity(wire::SessionContinuityResult {
                            state: match continuity.state {
                                ContinuityState::Warm => wire::SessionContinuityState::Warm,
                                ContinuityState::Cold => wire::SessionContinuityState::Cold,
                                ContinuityState::Lost => wire::SessionContinuityState::Lost,
                            },
                            generation: continuity.generation,
                            reason_code: None,
                        })
                    }
                    Err(error) => unavailable(&error, wire::QueryErrorCode::UnknownAgent),
                }
            }
            wire::Query::RuntimeDiagnostics(query) => {
                match QueryService::runtime_diagnostics(store, query.agent_id).await {
                    Ok(diagnostics) => {
                        wire::QueryResult::RuntimeDiagnostics(wire::RuntimeDiagnosticsResult {
                            local_run_id: diagnostics.local_run_id,
                            local_run_state: diagnostics.local_run_state.map(runtime_run_state),
                            queued_runs: diagnostics.queued_runs,
                            active_runs: diagnostics.active_runs,
                            pending_commands: diagnostics.pending_commands,
                            pending_result_events: diagnostics.pending_result_events,
                            warm_sessions: diagnostics.warm_sessions,
                            cold_sessions: diagnostics.cold_sessions,
                            reset_required_sessions: diagnostics.reset_required_sessions,
                            observed_at: OffsetDateTime::now_utc(),
                        })
                    }
                    Err(error) => unavailable(&error, wire::QueryErrorCode::UnknownAgent),
                }
            }
            wire::Query::MemoryList(query) => {
                match QueryService::memory_files(homes, query.agent_id).await {
                    Ok(files) => wire::QueryResult::MemoryList(wire::MemoryListResult {
                        files: files.iter().map(memory_projection).collect(),
                    }),
                    Err(error) => unavailable(&error, wire::QueryErrorCode::UnknownAgent),
                }
            }
            wire::Query::MemoryRead(query) => {
                match QueryService::memory_content(homes, query.agent_id, &query.path).await {
                    Ok((file, content)) => wire::QueryResult::MemoryRead(wire::MemoryReadResult {
                        file: memory_projection(&file),
                        content,
                    }),
                    Err(error) => unavailable(&error, wire::QueryErrorCode::UnknownPath),
                }
            }
            wire::Query::LlmUsage(query) => {
                match QueryService::llm_usage(store, query.range_hours).await {
                    Ok(summary) => wire::QueryResult::LlmUsage(wire::LlmUsageResult {
                        requests: summary.requests,
                        input_tokens: summary.input_tokens,
                        output_tokens: summary.output_tokens,
                        cached_input_tokens: summary.cached_input_tokens,
                        cache_write_tokens: summary.cache_write_tokens,
                        cache_hit_rate_basis_points: (summary.cache_hit_rate * 10_000.0).round()
                            as u64,
                        first_at: summary.first_at,
                        last_at: summary.last_at,
                        series: summary
                            .series
                            .into_iter()
                            .map(|bucket| wire::LlmUsageBucketResult {
                                bucket: bucket.bucket,
                                requests: bucket.requests,
                                input_tokens: bucket.input_tokens,
                                output_tokens: bucket.output_tokens,
                                cached_input_tokens: bucket.cached_input_tokens,
                            })
                            .collect(),
                        by_model: summary.by_model.into_iter().map(usage_breakdown).collect(),
                        by_agent: summary.by_agent.into_iter().map(usage_breakdown).collect(),
                        by_agent_series: summary
                            .by_agent_series
                            .into_iter()
                            .map(|entry| wire::LlmUsageAgentSeriesResult {
                                agent_id: entry.agent_id,
                                requests: entry.requests,
                                input_tokens: entry.input_tokens,
                                output_tokens: entry.output_tokens,
                                cached_input_tokens: entry.cached_input_tokens,
                                series: entry.series.into_iter().map(usage_bucket).collect(),
                            })
                            .collect(),
                        by_agent_model: summary
                            .by_agent_model
                            .into_iter()
                            .map(|entry| wire::LlmUsageAgentModelResult {
                                agent_id: entry.agent_id,
                                model: entry.model,
                                requests: entry.requests,
                                input_tokens: entry.input_tokens,
                                output_tokens: entry.output_tokens,
                                cached_input_tokens: entry.cached_input_tokens,
                            })
                            .collect(),
                    }),
                    Err(error) => {
                        tracing::warn!(%error, "LLM usage query failed on Computer");
                        unavailable(&error, wire::QueryErrorCode::Internal)
                    }
                }
            }
        };
        wire::ComputerFrame::QueryResult {
            result: wire::QueryResultEnvelope {
                query_id: envelope.query_id,
                result,
            },
        }
    }

    async fn application_command<P: TransactionPort, H: AgentHomePort>(
        &self,
        store: &mut P,
        homes: &mut H,
        command: wire::Command,
        channel_activity: Option<wire::ChannelActivitySnapshot>,
    ) -> Result<ApplicationCommand, ApplicationError> {
        match command {
            wire::Command::AgentProvision(configuration) => {
                let agent = local_agent(configuration);
                Ok(ApplicationCommand::Provision { agent })
            }
            wire::Command::AgentConfigure(configuration) => {
                let agent = local_agent(configuration);
                Ok(ApplicationCommand::Configure { agent })
            }
            wire::Command::AgentSuspend(suspend) => Ok(ApplicationCommand::Suspend {
                agent_id: suspend.agent_id,
                cancel_current: suspend.mode == wire::SuspendMode::CancelCurrentRun,
            }),
            wire::Command::AgentResume(resume) => Ok(ApplicationCommand::Resume {
                agent_id: resume.agent_id,
            }),
            wire::Command::AgentRestart(restart) => Ok(ApplicationCommand::Restart {
                agent_id: restart.agent_id,
            }),
            wire::Command::AgentRetire(retire) => Ok(ApplicationCommand::Retire {
                agent_id: retire.agent_id,
            }),
            wire::Command::RunStart(start) => {
                let agent = homes.agent(start.agent_id).await?;
                if agent.state != LocalAgentState::Active {
                    return Err(ApplicationError::Conflict);
                }
                let memory = homes
                    .list_memory(start.agent_id)
                    .await?
                    .into_iter()
                    .map(|file| MemoryEntryInput {
                        path: file.path,
                        size: file.size,
                        sha256: file.sha256,
                        updated_at: file.updated_at,
                    })
                    .collect::<Vec<_>>();
                let fingerprint = session_fingerprint(
                    &agent,
                    homes.workspace_fingerprint(agent.agent_id).await?,
                    task_threads(start.task.as_ref(), start.focus.thread_id),
                );
                let task_id = start.task.as_ref().map(|task| task.task_id);
                let dispatched_items = start
                    .dispatched_items
                    .iter()
                    .map(dispatched_item)
                    .collect::<Vec<_>>();
                let available_at = start
                    .dispatched_items
                    .iter()
                    .map(|item| item.available_at)
                    .min()
                    .unwrap_or_else(OffsetDateTime::now_utc);
                let strength = if start
                    .dispatched_items
                    .iter()
                    .any(|item| item.strength == wire::AttentionStrength::Hard)
                {
                    WorkStrength::Hard
                } else {
                    WorkStrength::Ambient
                };
                let focus_thread_id = start.focus.thread_id;
                let input = RunInput {
                    product_contract: self.product_contract.clone(),
                    agent: AgentInput {
                        agent_id: agent.agent_id,
                        space_id: agent.space_id,
                        identity: agent.name.clone(),
                        role_revision: agent.role_revision,
                        role: agent.role.clone(),
                        memory,
                    },
                    work: WorkInput {
                        task: start.task.as_ref().map(|task| TaskInput {
                            task_id: task.task_id,
                            seq: task.seq,
                            title: task.title.clone(),
                            status: task_status(task.status).to_owned(),
                        }),
                        linked_thread_ids: task_threads(start.task.as_ref(), focus_thread_id),
                        public_result_message_id: start
                            .task
                            .as_ref()
                            .and_then(|task| task.result_message_id),
                    },
                    context: RunContextInput {
                        focus_thread_id,
                        message_snapshot_sequence: start.focus.message_sequence,
                        focus_messages: std::iter::once(&start.focus.root)
                            .chain(start.focus.replies.iter())
                            .map(context_message)
                            .collect(),
                        channel_id: start.focus.channel_id,
                        channel_snapshot_sequence: start.channel_snapshot_sequence,
                        channel_activity: incremental_channel_activity(
                            store,
                            start.agent_id,
                            start.focus.channel_id,
                            focus_thread_id,
                            &dispatched_items,
                            channel_activity.as_ref(),
                        )
                        .await?,
                        dispatched_items,
                    },
                    channel_members: start
                        .channel_members
                        .iter()
                        .map(|member| ChannelMemberInput {
                            member_id: member.member_id,
                            display_name: member.display_name.clone(),
                        })
                        .collect(),
                };
                let run = LocalRun::new(NewRun {
                    id: start.run_id,
                    agent_id: start.agent_id,
                    task_id,
                    focus_thread_id,
                    priority: RunPriority {
                        explicit_human_redirect: false,
                        strength,
                        available_at,
                        has_task_continuity: task_id.is_some(),
                    },
                    input,
                })?;
                Ok(ApplicationCommand::Start {
                    run: Box::new(run),
                    fingerprint,
                })
            }
            wire::Command::RunTaskBound(bound) => {
                let run = load_run(store, bound.run_id).await?;
                let agent = homes.agent(run.view().agent_id).await?;
                Ok(ApplicationCommand::BindTask {
                    run_id: bound.run_id,
                    task_id: bound.task.task_id,
                    fingerprint: session_fingerprint(
                        &agent,
                        homes.workspace_fingerprint(agent.agent_id).await?,
                        bound.task.linked_thread_ids,
                    ),
                })
            }
            wire::Command::RunAttachItem(attach) => Ok(ApplicationCommand::Attach {
                run_id: attach.run_id,
                sequence: attach.delivery_sequence.0,
                item: dispatched_item(&attach.item),
            }),
            wire::Command::RunNotice(notice) => Ok(ApplicationCommand::Notice {
                run_id: notice.run_id,
                notice: attention_notice(notice.notice),
            }),
            wire::Command::RunStop(stop) => Ok(ApplicationCommand::Stop {
                run_id: stop.run_id,
            }),
            wire::Command::SessionReset(session) | wire::Command::SessionClose(session) => {
                let scope = local_scope(session.scope);
                let mut sessions = store
                    .transact(async |transaction| transaction.sessions(session.agent_id, scope))
                    .await?;
                let session = sessions
                    .drain(..)
                    .max_by_key(|session| session.view().generation)
                    .ok_or(ApplicationError::NotFound)?;
                Ok(ApplicationCommand::ResetSession { session })
            }
        }
    }

    pub(in crate::computer) fn event_frame(event: LocalEvent) -> wire::ComputerFrame {
        match event {
            LocalEvent::RunStarted { event_id, run_id } => wire::ComputerFrame::RunStarted {
                started: wire::RunStarted {
                    event_id,
                    run_id,
                    observed_at: OffsetDateTime::now_utc(),
                },
            },
            LocalEvent::Delivery {
                event_id,
                run_id,
                sequence,
                outcome,
            } => wire::ComputerFrame::DeliveryReceipt {
                receipt: wire::DeliveryReceipt {
                    event_id,
                    run_id,
                    delivery_sequence: wire::DeliverySequence(sequence),
                    outcome: match outcome {
                        DeliveryState::Accepted => wire::DeliveryOutcome::Accepted,
                        DeliveryState::TooLate => wire::DeliveryOutcome::TooLate,
                        DeliveryState::Unsupported => wire::DeliveryOutcome::Unsupported,
                        DeliveryState::Pending => wire::DeliveryOutcome::TooLate,
                    },
                },
            },
            LocalEvent::RunResult {
                event_id,
                run_id,
                status,
                item_outcomes,
                continuation_note,
                error_code,
            } => wire::ComputerFrame::RunResult {
                result: wire::RunResult {
                    event_id,
                    run_id,
                    status: terminal_status(status),
                    item_outcomes: item_outcomes
                        .into_iter()
                        .map(|(item_id, disposition)| wire::ItemOutcome {
                            item_id,
                            disposition: item_disposition(disposition),
                        })
                        .collect(),
                    continuation_note,
                    error_code: error_code.map(local_error),
                },
            },
        }
    }
}

async fn incremental_channel_activity<P: TransactionPort>(
    store: &mut P,
    agent_id: AgentId,
    channel_id: ChannelId,
    focus_thread_id: ThreadId,
    dispatched_items: &[DispatchedItemInput],
    snapshot: Option<&wire::ChannelActivitySnapshot>,
) -> Result<Vec<ChannelActivityInput>, ApplicationError> {
    let Some(snapshot) = snapshot else {
        return Ok(Vec::new());
    };
    let through = store
        .transact(async |transaction| {
            Ok(transaction
                .channel_context(agent_id, channel_id)?
                .map_or(0, |context| context.through_sequence))
        })
        .await?;
    let claimed_message_ids = dispatched_items
        .iter()
        .flat_map(|item| {
            item.message_id.into_iter().chain(
                item.activity_events
                    .iter()
                    .filter_map(|event| event.message_id),
            )
        })
        .collect::<HashSet<_>>();
    Ok(snapshot
        .messages
        .iter()
        .filter(|entry| {
            entry.message.sequence > through
                && entry.thread_id != focus_thread_id
                && !claimed_message_ids.contains(&entry.message.message_id)
        })
        .map(|entry| ChannelActivityInput {
            thread_id: entry.thread_id,
            channel_seq: entry.message.sequence,
            message: context_message(&entry.message),
        })
        .collect())
}

fn usage_breakdown(
    breakdown: crate::computer::application::usage::LlmUsageBreakdown,
) -> wire::LlmUsageBreakdownResult {
    wire::LlmUsageBreakdownResult {
        key: breakdown.key,
        requests: breakdown.requests,
        input_tokens: breakdown.input_tokens,
        output_tokens: breakdown.output_tokens,
        cached_input_tokens: breakdown.cached_input_tokens,
    }
}

fn usage_bucket(
    bucket: crate::computer::application::usage::LlmUsageBucket,
) -> wire::LlmUsageBucketResult {
    wire::LlmUsageBucketResult {
        bucket: bucket.bucket,
        requests: bucket.requests,
        input_tokens: bucket.input_tokens,
        output_tokens: bucket.output_tokens,
        cached_input_tokens: bucket.cached_input_tokens,
    }
}

async fn load_run<P: TransactionPort>(
    store: &mut P,
    run_id: RunId,
) -> Result<LocalRun, ApplicationError> {
    store
        .transact(async |transaction| transaction.run(run_id)?.ok_or(ApplicationError::NotFound))
        .await
}

fn local_agent(configuration: wire::AgentConfiguration) -> LocalAgent {
    LocalAgent {
        agent_id: configuration.agent_id,
        space_id: configuration.space_id,
        name: configuration.name,
        role_revision: configuration.role.revision,
        role: configuration.role.text,
        driver: match configuration.driver {
            wire::DriverKind::Codex => DriverKind::Codex,
            wire::DriverKind::Builtin => DriverKind::Builtin,
        },
        state: LocalAgentState::Active,
    }
}

fn task_threads(task: Option<&wire::TaskSnapshot>, focus: ThreadId) -> Vec<ThreadId> {
    task.map_or_else(|| vec![focus], |task| task.linked_thread_ids.clone())
}

fn session_fingerprint(
    agent: &LocalAgent,
    workspace: String,
    mut threads: Vec<ThreadId>,
) -> SessionFingerprint {
    threads.sort_unstable();
    let mut digest = Sha256::new();
    for thread in threads {
        digest.update(thread.to_string().as_bytes());
    }
    SessionFingerprint {
        driver: agent.driver,
        workspace,
        role_revision: agent.role_revision,
        audience: hex::encode(digest.finalize()),
    }
}

fn dispatched_item(item: &wire::InboxItemSnapshot) -> DispatchedItemInput {
    DispatchedItemInput {
        item_id: item.item_id,
        source_kind: inbox_source_kind(item.source_kind).to_owned(),
        strength: item_strength(item.strength),
        task_id: item.task_id,
        channel_id: item.channel_id,
        thread_id: item.thread_id,
        message_id: item.message.as_ref().map(|message| message.message_id),
        content: item.message.as_ref().map(message_content),
        activity_events: item
            .activity_events
            .iter()
            .map(|event| ActivityEventInput {
                sequence: event.sequence,
                kind: activity_event_kind(event.kind).to_owned(),
                message_id: event.message_id,
                member_id: event.member_id,
            })
            .collect(),
    }
}

fn activity_event_kind(kind: wire::ActivityEventKind) -> &'static str {
    match kind {
        wire::ActivityEventKind::Message => "message",
        wire::ActivityEventKind::MemberJoined => "member_joined",
        wire::ActivityEventKind::MemberLeft => "member_left",
    }
}

fn inbox_source_kind(kind: wire::InboxSourceKind) -> &'static str {
    match kind {
        wire::InboxSourceKind::Direct => "direct",
        wire::InboxSourceKind::Mention => "mention",
        wire::InboxSourceKind::Reply => "reply",
        wire::InboxSourceKind::TaskActivity => "task_activity",
        wire::InboxSourceKind::ThreadActivity => "thread_activity",
        wire::InboxSourceKind::ChannelActivity => "channel_activity",
        wire::InboxSourceKind::System => "system",
    }
}

fn item_strength(strength: wire::AttentionStrength) -> WorkStrength {
    match strength {
        wire::AttentionStrength::Hard => WorkStrength::Hard,
        wire::AttentionStrength::Ambient => WorkStrength::Ambient,
    }
}

fn context_message(message: &wire::MessageSnapshot) -> ContextMessageInput {
    ContextMessageInput {
        message_id: message.message_id,
        author_member_id: message.author_member_id,
        body: message_content(message),
    }
}

fn message_content(message: &wire::MessageSnapshot) -> String {
    match &message.content {
        wire::MessageContent::Text { markdown } => markdown.clone(),
        wire::MessageContent::Action { action, target } => format!("{action:?}:{target:?}"),
    }
}

fn attention_notice(notice: wire::AttentionNotice) -> AttentionNoticeInput {
    AttentionNoticeInput {
        notice_id: notice.notice_id,
        source_kind: format!("{:?}", notice.source_kind).to_lowercase(),
        location: match notice.location {
            wire::NoticeLocation::Restricted => NoticeLocationInput::Restricted,
            wire::NoticeLocation::Visible { task_id, thread_id } => {
                NoticeLocationInput::Visible { task_id, thread_id }
            }
        },
        explicit_human_redirect: notice.explicit_human_redirect,
        arrived_at: notice.arrived_at,
    }
}

fn local_scope(scope: wire::SessionScope) -> SessionScope {
    match scope {
        wire::SessionScope::Thread(id) => SessionScope::Thread(id),
        wire::SessionScope::Task(id) => SessionScope::Task(id),
    }
}

fn runtime_run_state(state: LocalRunState) -> wire::RuntimeRunState {
    match state {
        LocalRunState::Queued => wire::RuntimeRunState::Queued,
        LocalRunState::Starting => wire::RuntimeRunState::Starting,
        LocalRunState::Running => wire::RuntimeRunState::Running,
        LocalRunState::Finalizing => wire::RuntimeRunState::Finalizing,
        LocalRunState::Stopping => wire::RuntimeRunState::Stopping,
        LocalRunState::Completed
        | LocalRunState::Yielded
        | LocalRunState::Failed
        | LocalRunState::Canceled => {
            unreachable!("terminal local Run state was returned as a runtime diagnostic")
        }
    }
}

fn task_status(status: wire::TaskStatus) -> &'static str {
    match status {
        wire::TaskStatus::Todo => "todo",
        wire::TaskStatus::InProgress => "in_progress",
        wire::TaskStatus::InReview => "in_review",
        wire::TaskStatus::Done => "done",
        wire::TaskStatus::Closed => "closed",
    }
}

fn terminal_status(status: TerminalStatus) -> wire::RunTerminalStatus {
    match status {
        TerminalStatus::Completed => wire::RunTerminalStatus::Completed,
        TerminalStatus::Yielded => wire::RunTerminalStatus::Yielded,
        TerminalStatus::Failed => wire::RunTerminalStatus::Failed,
        TerminalStatus::Canceled => wire::RunTerminalStatus::Canceled,
    }
}

fn item_disposition(disposition: ItemDisposition) -> wire::ItemDisposition {
    match disposition {
        ItemDisposition::Handled => wire::ItemDisposition::Handled,
        ItemDisposition::Deferred => wire::ItemDisposition::Deferred,
        ItemDisposition::Released => wire::ItemDisposition::Released,
    }
}

fn local_error(error: LocalErrorCode) -> wire::ComputerErrorCode {
    match error {
        LocalErrorCode::DriverError => wire::ComputerErrorCode::DriverError,
        LocalErrorCode::DriverLost => wire::ComputerErrorCode::DriverLost,
        LocalErrorCode::ComputerRestarted => wire::ComputerErrorCode::ComputerRestarted,
        LocalErrorCode::SessionUnavailable => wire::ComputerErrorCode::SessionUnavailable,
        LocalErrorCode::UnhandledItems => wire::ComputerErrorCode::UnhandledItems,
        LocalErrorCode::Internal => wire::ComputerErrorCode::Internal,
    }
}

fn memory_projection(file: &MemoryFile) -> wire::MemoryFileProjection {
    wire::MemoryFileProjection {
        path: file.path.clone(),
        size: file.size,
        sha256: file.sha256.clone(),
        updated_at: file.updated_at,
    }
}

fn unavailable(error: &ApplicationError, missing: wire::QueryErrorCode) -> wire::QueryResult {
    wire::QueryResult::Unavailable {
        code: match error {
            ApplicationError::NotFound | ApplicationError::Conflict => missing,
            ApplicationError::SessionLost => wire::QueryErrorCode::SessionLost,
            ApplicationError::DriverUnavailable => wire::QueryErrorCode::DriverUnavailable,
            ApplicationError::AlreadyApplied
            | ApplicationError::Unauthenticated
            | ApplicationError::Core(_)
            | ApplicationError::Internal => wire::QueryErrorCode::Internal,
        },
    }
}

fn computer_error(error: &ApplicationError) -> wire::ComputerErrorCode {
    match error {
        ApplicationError::NotFound
        | ApplicationError::Conflict
        | ApplicationError::AlreadyApplied => wire::ComputerErrorCode::InvalidCommand,
        ApplicationError::DriverUnavailable => wire::ComputerErrorCode::DriverLost,
        ApplicationError::SessionLost => wire::ComputerErrorCode::SessionUnavailable,
        ApplicationError::Unauthenticated
        | ApplicationError::Core(_)
        | ApplicationError::Internal => wire::ComputerErrorCode::Internal,
    }
}

#[cfg(test)]
mod tests {
    use async_trait::async_trait;
    use time::OffsetDateTime;
    use uuid::Uuid;

    use super::*;
    use crate::{
        computer::{
            adapters::{filesystem::AgentHomeAdapter, sqlite::SqliteAdapter},
            application::ports::{
                ChannelContextState, DriverCompletion, DriverTurnOutcome, OpenSessionRequest,
                OpenedSession, ProcessEvidence, SteerOutcome, TransactionPort,
            },
            application::{LocalRun, ProviderSession},
        },
        ids::{
            AgentId, ChannelId, CommandId, InboxItemId, MemberId, MessageId, RunId, SpaceId,
            ThreadId,
        },
        protocol::computer::{
            AgentConfiguration, AttentionStrength, ChannelActivityMessageSnapshot,
            ChannelActivitySnapshot, CommandEnvelope, CommandSequence,
            DriverKind as WireDriverKind, FocusSnapshot, InboxItemSnapshot, InboxSourceKind,
            MessageContent, MessageSnapshot, RoleSnapshot, RunStart,
        },
    };

    struct NoopDriver;

    #[test]
    fn dispatched_item_preserves_source_kind_and_strength() {
        let item = dispatched_item(&InboxItemSnapshot {
            item_id: InboxItemId::from_uuid(Uuid::now_v7()),
            source_kind: InboxSourceKind::ChannelActivity,
            strength: AttentionStrength::Ambient,
            channel_id: ChannelId::from_uuid(Uuid::now_v7()),
            thread_id: ThreadId::from_uuid(Uuid::now_v7()),
            task_id: None,
            message: None,
            activity_events: Vec::new(),
            available_at: OffsetDateTime::now_utc(),
        });

        assert_eq!(item.source_kind, "channel_activity");
        assert_eq!(item.strength, WorkStrength::Ambient);
    }

    #[tokio::test]
    async fn incremental_channel_activity_projects_only_new_unclaimed_non_focus_messages() {
        let directory = tempfile::tempdir().unwrap();
        let mut store = SqliteAdapter::open(&directory.path().join("daemon.db"))
            .await
            .unwrap();
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let channel_id = ChannelId::from_uuid(Uuid::now_v7());
        let focus_thread_id = ThreadId::from_uuid(Uuid::now_v7());
        let old_message_id = MessageId::from_uuid(Uuid::now_v7());
        let claimed_item_message_id = MessageId::from_uuid(Uuid::now_v7());
        let claimed_activity_message_id = MessageId::from_uuid(Uuid::now_v7());
        let other_message_id = MessageId::from_uuid(Uuid::now_v7());

        store
            .transact(async |transaction| {
                transaction.save_channel_context(ChannelContextState {
                    agent_id,
                    channel_id,
                    through_sequence: 10,
                })
            })
            .await
            .unwrap();

        let snapshot_message = |thread_id: ThreadId, message_id: MessageId, sequence: u64| {
            ChannelActivityMessageSnapshot {
                thread_id,
                message: MessageSnapshot {
                    message_id,
                    author_member_id: MemberId::from_uuid(Uuid::now_v7()),
                    sequence,
                    content: MessageContent::Text {
                        markdown: format!("message-{sequence}"),
                    },
                    created_at: OffsetDateTime::now_utc(),
                },
            }
        };
        let snapshot = ChannelActivitySnapshot {
            channel_id,
            snapshot_sequence: 14,
            messages: vec![
                snapshot_message(focus_thread_id, MessageId::from_uuid(Uuid::now_v7()), 11),
                snapshot_message(ThreadId::from_uuid(Uuid::now_v7()), old_message_id, 10),
                snapshot_message(
                    ThreadId::from_uuid(Uuid::now_v7()),
                    claimed_item_message_id,
                    12,
                ),
                snapshot_message(
                    ThreadId::from_uuid(Uuid::now_v7()),
                    claimed_activity_message_id,
                    13,
                ),
                snapshot_message(ThreadId::from_uuid(Uuid::now_v7()), other_message_id, 14),
            ],
        };
        let dispatched_items = vec![
            DispatchedItemInput {
                item_id: InboxItemId::from_uuid(Uuid::now_v7()),
                source_kind: "mention".to_owned(),
                strength: WorkStrength::Hard,
                task_id: None,
                channel_id,
                thread_id: ThreadId::from_uuid(Uuid::now_v7()),
                message_id: Some(claimed_item_message_id),
                content: None,
                activity_events: Vec::new(),
            },
            DispatchedItemInput {
                item_id: InboxItemId::from_uuid(Uuid::now_v7()),
                source_kind: "channel_activity".to_owned(),
                strength: WorkStrength::Ambient,
                task_id: None,
                channel_id,
                thread_id: ThreadId::from_uuid(Uuid::now_v7()),
                message_id: None,
                content: None,
                activity_events: vec![ActivityEventInput {
                    sequence: 13,
                    kind: "message".to_owned(),
                    message_id: Some(claimed_activity_message_id),
                    member_id: None,
                }],
            },
        ];

        let projected = incremental_channel_activity(
            &mut store,
            agent_id,
            channel_id,
            focus_thread_id,
            &dispatched_items,
            Some(&snapshot),
        )
        .await
        .unwrap();
        assert_eq!(projected.len(), 1);
        assert_eq!(projected[0].channel_seq, 14);
        assert_eq!(projected[0].thread_id, snapshot.messages[4].thread_id);
        assert_eq!(projected[0].message.message_id, other_message_id);
        assert!(
            !projected
                .iter()
                .any(|message| message.message.message_id == old_message_id)
        );
    }

    #[async_trait(?Send)]
    impl DriverPort for NoopDriver {
        async fn validate(&mut self, _: &LocalAgent) -> Result<(), ApplicationError> {
            Ok(())
        }

        async fn open_or_resume(
            &mut self,
            _: OpenSessionRequest,
        ) -> Result<OpenedSession, ApplicationError> {
            Err(ApplicationError::DriverUnavailable)
        }

        async fn start_turn(&mut self, _: &LocalRun, _: &str) -> Result<(), ApplicationError> {
            Ok(())
        }

        async fn steer(&mut self, _: &LocalRun, _: u64) -> Result<SteerOutcome, ApplicationError> {
            Ok(SteerOutcome::Unsupported)
        }

        async fn notice(
            &mut self,
            _: &LocalRun,
            _: &AttentionNoticeInput,
        ) -> Result<(), ApplicationError> {
            Ok(())
        }

        async fn interrupt(&mut self, _: &LocalRun) -> Result<(), ApplicationError> {
            Ok(())
        }

        async fn wait_for_completion(
            &mut self,
            _: RunId,
            _: std::time::Duration,
        ) -> Result<Option<DriverTurnOutcome>, ApplicationError> {
            Ok(None)
        }

        async fn restart_agent(&mut self, _: AgentId) -> Result<(), ApplicationError> {
            Ok(())
        }

        async fn close_session(&mut self, _: &ProviderSession) -> Result<(), ApplicationError> {
            Ok(())
        }

        async fn process_evidence(
            &mut self,
            _: &LocalRun,
        ) -> Result<ProcessEvidence, ApplicationError> {
            Ok(ProcessEvidence::Lost)
        }

        async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
            Ok(Vec::new())
        }
    }

    #[tokio::test]
    async fn run_start_uses_persisted_agent_profile_and_keeps_wire_content_out_of_debug() {
        let directory = tempfile::tempdir().unwrap();
        let mut store = SqliteAdapter::open(&directory.path().join("daemon.db"))
            .await
            .unwrap();
        let mut homes = AgentHomeAdapter::new(directory.path().join("computer"), None, None);
        let adapter = ServerConnectionAdapter::new("contract".to_owned());
        let mut driver = NoopDriver;
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let space_id = SpaceId::from_uuid(Uuid::now_v7());
        adapter
            .receive(
                &mut store,
                &mut driver,
                &mut homes,
                CommandEnvelope {
                    command_id: CommandId::from_uuid(Uuid::now_v7()),
                    sequence: CommandSequence(1),
                    command: wire::Command::AgentProvision(AgentConfiguration {
                        agent_id,
                        space_id,
                        name: "Sumi".to_owned(),
                        role: RoleSnapshot {
                            revision: 3,
                            text: "role".to_owned(),
                        },
                        driver: WireDriverKind::Codex,
                    }),
                },
            )
            .await
            .unwrap();

        let thread_id = ThreadId::from_uuid(Uuid::now_v7());
        let message = MessageSnapshot {
            message_id: MessageId::from_uuid(Uuid::now_v7()),
            author_member_id: MemberId::from_uuid(Uuid::now_v7()),
            sequence: 1,
            content: MessageContent::Text {
                markdown: "private body".to_owned(),
            },
            created_at: OffsetDateTime::now_utc(),
        };
        let run_id = RunId::from_uuid(Uuid::now_v7());
        let frames = adapter
            .receive(
                &mut store,
                &mut driver,
                &mut homes,
                CommandEnvelope {
                    command_id: CommandId::from_uuid(Uuid::now_v7()),
                    sequence: CommandSequence(2),
                    command: wire::Command::RunStart(RunStart {
                        run_id,
                        agent_id,
                        task: None,
                        focus: FocusSnapshot {
                            thread_id,
                            channel_id: ChannelId::from_uuid(Uuid::now_v7()),
                            root: message,
                            replies: Vec::new(),
                            message_sequence: 1,
                        },
                        channel_snapshot_sequence: 1,
                        dispatched_items: Vec::new(),
                        channel_members: Vec::new(),
                    }),
                },
            )
            .await
            .unwrap();
        assert!(matches!(frames[0], wire::ComputerFrame::CommandAck { .. }));
        let run = store
            .transact(async |transaction| transaction.run(run_id))
            .await
            .unwrap()
            .unwrap();
        assert_eq!(run.view().input.agent.role_revision, 3);
        assert_eq!(run.view().input.context.focus_thread_id, thread_id);
        assert!(!format!("{run:?}").contains("private body"));
        assert_eq!(run.view().priority.strength, WorkStrength::Ambient);
    }

    #[test]
    fn activity_event_kinds_keep_the_wire_snake_case() {
        assert_eq!(
            activity_event_kind(wire::ActivityEventKind::Message),
            "message"
        );
        assert_eq!(
            activity_event_kind(wire::ActivityEventKind::MemberJoined),
            "member_joined"
        );
        assert_eq!(
            activity_event_kind(wire::ActivityEventKind::MemberLeft),
            "member_left"
        );
    }
}
