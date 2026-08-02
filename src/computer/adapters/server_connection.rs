use sha2::{Digest, Sha256};
use time::OffsetDateTime;

use crate::{
    computer::application::{
        AgentInput, ApplicationError, AttentionNoticeInput, ClaimedItemInput, ContextMessageInput,
        ContinuityState, DeliveryState, DriverKind, FencingToken, ItemDisposition, LocalAgent,
        LocalAgentState, LocalRun, MemoryEntryInput, MemoryFile, NewRun, NoticeLocationInput,
        RunContextInput, RunInput, RunPriority, SessionFingerprint, SessionScope, TaskInput,
        TerminalStatus, WorkInput, WorkStrength,
        command::{Command as ApplicationCommand, CommandService},
        ports::{
            AgentHomePort, CommandStatus, ComputerTransaction, DriverPort, LocalErrorCode,
            LocalEvent, TransactionPort,
        },
        query::QueryService,
    },
    ids::{RunId, ThreadId},
    protocol::computer as wire,
};

pub(in crate::computer) struct ServerConnectionAdapter {
    global_contract: String,
}

impl ServerConnectionAdapter {
    pub(in crate::computer) fn new(global_contract: String) -> Self {
        Self { global_contract }
    }

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
        let command = self
            .application_command(store, homes, envelope.command)
            .await?;
        let sequence = envelope.sequence.0;
        let execution =
            CommandService::execute(store, driver, homes, envelope.command_id, sequence, command)
                .await?;
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

    pub(in crate::computer) async fn answer_query<P: TransactionPort, H: AgentHomePort>(
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
                let claimed_items = start
                    .claimed_items
                    .iter()
                    .map(claimed_item)
                    .collect::<Vec<_>>();
                let available_at = start
                    .claimed_items
                    .iter()
                    .map(|item| item.available_at)
                    .min()
                    .unwrap_or_else(OffsetDateTime::now_utc);
                let strength = if start
                    .claimed_items
                    .iter()
                    .any(|item| item.strength == wire::AttentionStrength::Hard)
                {
                    WorkStrength::Hard
                } else {
                    WorkStrength::Ambient
                };
                let focus_thread_id = start.focus.thread_id;
                let input = RunInput {
                    global_contract: self.global_contract.clone(),
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
                        claimed_items,
                    },
                };
                let run = LocalRun::new(NewRun {
                    id: start.run_id,
                    agent_id: start.agent_id,
                    task_id,
                    focus_thread_id,
                    fencing_token: FencingToken::new(start.fencing_token.expose().to_owned()),
                    priority: RunPriority {
                        explicit_human_redirect: false,
                        strength,
                        available_at,
                        has_task_continuity: task_id.is_some(),
                    },
                    ownership_lease_expires_at: start.ownership_lease_expires_at,
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
                item: claimed_item(&attach.item),
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
            LocalEvent::RunStarted {
                event_id,
                run_id,
                fencing_token,
            } => wire::ComputerFrame::RunStarted {
                started: wire::RunStarted {
                    event_id,
                    run_id,
                    fencing_token: wire::FencingToken::new(fencing_token.expose().to_owned()),
                    observed_at: OffsetDateTime::now_utc(),
                },
            },
            LocalEvent::Delivery {
                event_id,
                run_id,
                sequence,
                outcome,
                fencing_token,
            } => wire::ComputerFrame::DeliveryReceipt {
                receipt: wire::DeliveryReceipt {
                    event_id,
                    run_id,
                    delivery_sequence: wire::DeliverySequence(sequence),
                    fencing_token: wire::FencingToken::new(fencing_token.expose().to_owned()),
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
                fencing_token,
            } => wire::ComputerFrame::RunResult {
                result: wire::RunResult {
                    event_id,
                    run_id,
                    fencing_token: wire::FencingToken::new(fencing_token.expose().to_owned()),
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

fn claimed_item(item: &wire::InboxItemSnapshot) -> ClaimedItemInput {
    ClaimedItemInput {
        item_id: item.item_id,
        task_id: item.task_id,
        thread_id: item.thread_id,
        message_id: item.message.as_ref().map(|message| message.message_id),
        content: item.message.as_ref().map(message_content),
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
        LocalErrorCode::ProcessLost => wire::ComputerErrorCode::ProcessLost,
        LocalErrorCode::SessionLost => wire::ComputerErrorCode::SessionLost,
        LocalErrorCode::DriverUnavailable => wire::ComputerErrorCode::DriverUnavailable,
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
        ApplicationError::DriverUnavailable => wire::ComputerErrorCode::DriverUnavailable,
        ApplicationError::SessionLost => wire::ComputerErrorCode::SessionLost,
        ApplicationError::Unauthenticated
        | ApplicationError::Core(_)
        | ApplicationError::Internal => wire::ComputerErrorCode::Internal,
    }
}

#[cfg(test)]
mod tests {
    use async_trait::async_trait;
    use time::{Duration, OffsetDateTime};
    use uuid::Uuid;

    use super::*;
    use crate::{
        computer::{
            adapters::{filesystem::AgentHomeAdapter, sqlite::SqliteAdapter},
            application::ports::{
                DriverCompletion, OpenSessionRequest, OpenedSession, ProcessEvidence, SteerOutcome,
            },
            application::{LocalRun, ProviderSession},
        },
        ids::{AgentId, ChannelId, CommandId, MemberId, MessageId, RunId, SpaceId, ThreadId},
        protocol::computer::{
            AgentConfiguration, CommandEnvelope, CommandSequence, DriverKind as WireDriverKind,
            FencingToken as WireFencingToken, FocusSnapshot, MessageContent, MessageSnapshot,
            RoleSnapshot, RunStart,
        },
    };

    struct NoopDriver;

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

        async fn notice(&mut self, _: &LocalRun) -> Result<(), ApplicationError> {
            Ok(())
        }

        async fn interrupt(&mut self, _: &LocalRun) -> Result<(), ApplicationError> {
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
                        claimed_items: Vec::new(),
                        fencing_token: WireFencingToken::new("raw-token".to_owned()),
                        ownership_lease_expires_at: OffsetDateTime::now_utc()
                            + Duration::minutes(5),
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
        assert!(!format!("{run:?}").contains("raw-token"));
        assert_eq!(run.view().priority.strength, WorkStrength::Ambient);
    }
}
