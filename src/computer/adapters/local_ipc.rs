use std::path::{Path, PathBuf};

use serde::{Serialize, de::DeserializeOwned};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};

use crate::{
    computer::application::{
        ApplicationError,
        capability::{CapabilityService, ScopeRequirement},
        ports::TransactionPort,
    },
    protocol::capability as wire,
};

const MAX_FRAME_BYTES: usize = 1024 * 1024;

pub(in crate::computer) struct LocalIpcAdapter {
    #[cfg(unix)]
    listener: tokio::net::UnixListener,
    path: PathBuf,
}

impl LocalIpcAdapter {
    #[cfg(unix)]
    pub(in crate::computer) async fn bind(path: &Path) -> Result<Self, ApplicationError> {
        if let Some(parent) = path.parent() {
            tokio::fs::create_dir_all(parent)
                .await
                .map_err(|_| ApplicationError::Internal)?;
        }
        if let Ok(metadata) = tokio::fs::symlink_metadata(path).await {
            use std::os::unix::fs::FileTypeExt;
            if !metadata.file_type().is_socket() {
                return Err(ApplicationError::Conflict);
            }
            tokio::fs::remove_file(path)
                .await
                .map_err(|_| ApplicationError::Internal)?;
        }
        let listener =
            tokio::net::UnixListener::bind(path).map_err(|_| ApplicationError::Internal)?;
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600))
            .await
            .map_err(|_| ApplicationError::Internal)?;
        Ok(Self {
            listener,
            path: path.to_path_buf(),
        })
    }

    #[cfg(unix)]
    pub(in crate::computer) async fn serve_one<Req, Res>(
        &self,
        handler: impl AsyncFnOnce(Req) -> Res,
    ) -> Result<(), ApplicationError>
    where
        Req: DeserializeOwned,
        Res: Serialize,
    {
        let (stream, _) = self
            .listener
            .accept()
            .await
            .map_err(|_| ApplicationError::Internal)?;
        let (reader, mut writer) = stream.into_split();
        let reader = BufReader::new(reader);
        let mut frame = Vec::new();
        reader
            .take(MAX_FRAME_BYTES as u64 + 1)
            .read_until(b'\n', &mut frame)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        if frame.len() > MAX_FRAME_BYTES {
            return Err(ApplicationError::Conflict);
        }
        let request = serde_json::from_slice(&frame).map_err(|_| ApplicationError::Conflict)?;
        let response = handler(request).await;
        let mut encoded = serde_json::to_vec(&response).map_err(|_| ApplicationError::Internal)?;
        encoded.push(b'\n');
        writer
            .write_all(&encoded)
            .await
            .map_err(|_| ApplicationError::Internal)
    }

    #[cfg(unix)]
    pub(in crate::computer) async fn serve_capability<P: TransactionPort>(
        &self,
        store: &mut P,
        forward: impl AsyncFnOnce(wire::RunContext, wire::Action) -> wire::Response<serde_json::Value>,
    ) -> Result<(), ApplicationError> {
        self.serve_one(|request: wire::Request| async move {
            if request.schema_version != wire::SCHEMA_VERSION {
                return failure(
                    wire::ErrorCode::InvalidArgument,
                    "unsupported capability schema version",
                    false,
                );
            }
            let path = match &request.action {
                wire::Action::AttachmentUpload { path }
                | wire::Action::MemoryRead { path }
                | wire::Action::MemoryWrite { path, .. } => Some(path.as_str()),
                wire::Action::AttachmentDownload { output, .. } => Some(output.as_str()),
                _ => None,
            };
            if path.is_some_and(|path| CapabilityService::validate_agent_path(path).is_err()) {
                return failure(
                    wire::ErrorCode::InvalidArgument,
                    "path must be relative to the current Agent Home",
                    false,
                );
            }
            let requirement = if request.action.requires_task() {
                ScopeRequirement::BoundTask
            } else {
                ScopeRequirement::CurrentRun
            };
            let context =
                match CapabilityService::authorize(store, &request.run_token, requirement).await {
                    Ok(context) => context,
                    Err(error) => return application_failure(error),
                };
            forward(
                wire::RunContext {
                    agent_id: context.agent_id,
                    space_id: context.space_id,
                    task_id: context.task_id,
                    focus_thread_id: context.focus_thread_id,
                    run_id: context.run_id,
                    fencing_token: context.fencing_token,
                    message_snapshot_sequence: context.message_snapshot_sequence,
                },
                request.action,
            )
            .await
        })
        .await
    }
}

fn application_failure(error: ApplicationError) -> wire::Response<serde_json::Value> {
    match error {
        ApplicationError::Unauthenticated => failure(
            wire::ErrorCode::Unauthenticated,
            "run capability is invalid or expired",
            false,
        ),
        ApplicationError::Conflict | ApplicationError::Core(_) => failure(
            wire::ErrorCode::Conflict,
            "capability conflicts with the current Run context",
            false,
        ),
        ApplicationError::NotFound => {
            failure(wire::ErrorCode::NotFound, "resource was not found", false)
        }
        ApplicationError::DriverUnavailable | ApplicationError::SessionLost => failure(
            wire::ErrorCode::Unavailable,
            "local capability service is unavailable",
            true,
        ),
        ApplicationError::AlreadyApplied | ApplicationError::Internal => failure(
            wire::ErrorCode::Internal,
            "local capability service failed",
            false,
        ),
    }
}

fn failure(
    code: wire::ErrorCode,
    message: &str,
    retryable: bool,
) -> wire::Response<serde_json::Value> {
    wire::Response::failure(wire::Error {
        code,
        message: message.to_owned(),
        retryable,
        details: Default::default(),
    })
}

impl Drop for LocalIpcAdapter {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.path);
    }
}

#[cfg(all(test, unix))]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use super::*;
    use crate::{
        agent_cli::client,
        computer::{
            adapters::sqlite::SqliteAdapter,
            application::{
                AgentInput, ClaimedItemInput, FencingToken, LocalRun, LocalRunState, NewRun,
                RunContextInput, RunInput, RunPriority, TaskInput, WorkInput, WorkStrength,
                ports::{ComputerTransaction, TransactionPort},
            },
        },
        ids::{AgentId, RunId, SpaceId, TaskId, ThreadId},
        protocol::capability::{Action, Response},
    };
    use time::{Duration, OffsetDateTime};
    use uuid::Uuid;

    #[tokio::test]
    async fn socket_is_private_and_removed_with_adapter() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("runtime/daemon.sock");
        let adapter = LocalIpcAdapter::bind(&path).await.unwrap();
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        drop(adapter);
        assert!(!path.exists());
    }

    #[tokio::test]
    async fn bind_refuses_to_delete_a_non_socket_target() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("daemon.sock");
        tokio::fs::write(&path, b"keep").await.unwrap();

        assert!(matches!(
            LocalIpcAdapter::bind(&path).await,
            Err(ApplicationError::Conflict)
        ));
        assert_eq!(tokio::fs::read(&path).await.unwrap(), b"keep");
    }

    #[tokio::test]
    async fn capability_flow_injects_context_from_authenticated_run() {
        let directory = tempfile::tempdir().unwrap();
        let socket_path = directory.path().join("daemon.sock");
        let adapter = LocalIpcAdapter::bind(&socket_path).await.unwrap();
        let mut store = SqliteAdapter::open(&directory.path().join("daemon.db"))
            .await
            .unwrap();
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let space_id = SpaceId::from_uuid(Uuid::now_v7());
        let task_id = TaskId::from_uuid(Uuid::now_v7());
        let thread_id = ThreadId::from_uuid(Uuid::now_v7());
        let run_id = RunId::from_uuid(Uuid::now_v7());
        let mut run = LocalRun::new(NewRun {
            id: run_id,
            agent_id,
            task_id: Some(task_id),
            focus_thread_id: thread_id,
            fencing_token: FencingToken::new("run-secret".to_owned()),
            priority: RunPriority {
                explicit_human_redirect: false,
                strength: WorkStrength::Hard,
                available_at: OffsetDateTime::now_utc(),
                has_task_continuity: true,
            },
            ownership_lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(5),
            input: RunInput {
                global_contract: "contract".to_owned(),
                agent: AgentInput {
                    agent_id,
                    space_id,
                    identity: "agent".to_owned(),
                    role_revision: 1,
                    role: "role".to_owned(),
                    memory_entry: "memory/".to_owned(),
                },
                work: WorkInput {
                    task: Some(TaskInput {
                        task_id,
                        title: "task".to_owned(),
                        status: "in_progress".to_owned(),
                    }),
                    linked_thread_ids: vec![thread_id],
                    public_result_message_id: None,
                },
                context: RunContextInput {
                    focus_thread_id: thread_id,
                    message_snapshot_sequence: 9,
                    focus_messages: Vec::new(),
                    claimed_items: Vec::<ClaimedItemInput>::new(),
                },
            },
        })
        .unwrap();
        run.state = LocalRunState::Running;
        store
            .transact(async |transaction| transaction.save_run(run))
            .await
            .unwrap();

        let server = adapter.serve_capability(
            &mut store,
            |context: crate::protocol::capability::RunContext, action: Action| async move {
                assert_eq!(context.agent_id, agent_id);
                assert_eq!(context.space_id, space_id);
                assert_eq!(context.task_id, Some(task_id));
                assert_eq!(context.focus_thread_id, thread_id);
                assert_eq!(context.run_id, run_id);
                assert_eq!(context.message_snapshot_sequence, 9);
                assert!(matches!(action, Action::TaskUpdate { .. }));
                Response::success(serde_json::json!({ "forwarded": true }))
            },
        );
        let client = client::call_with(
            &socket_path,
            "run-secret".to_owned(),
            Action::TaskUpdate {
                title: "new title".to_owned(),
            },
            None,
        );
        let (served, response) = tokio::join!(server, client);
        served.unwrap();
        let response = response.unwrap();
        assert!(response.ok);
        assert_eq!(response.data.unwrap()["forwarded"], true);

        let rejected_server = adapter.serve_capability(
            &mut store,
            |_: crate::protocol::capability::RunContext, _: Action| async move {
                panic!("unauthenticated capability must not be forwarded")
            },
        );
        let rejected_client = client::call_with(
            &socket_path,
            "forged-token".to_owned(),
            Action::ContextCurrent,
            None,
        );
        let (served, response) = tokio::join!(rejected_server, rejected_client);
        served.unwrap();
        let response = response.unwrap();
        assert!(!response.ok);
        assert_eq!(
            response.error.unwrap().code,
            wire::ErrorCode::Unauthenticated
        );

        let path_server = adapter.serve_capability(
            &mut store,
            |_: crate::protocol::capability::RunContext, _: Action| async move {
                panic!("unsafe local path must not be forwarded")
            },
        );
        let path_client = client::call_with(
            &socket_path,
            "run-secret".to_owned(),
            Action::MemoryRead {
                path: "../other-agent/memory.md".to_owned(),
            },
            None,
        );
        let (served, response) = tokio::join!(path_server, path_client);
        served.unwrap();
        let response = response.unwrap();
        assert_eq!(
            response.error.unwrap().code,
            wire::ErrorCode::InvalidArgument
        );
    }
}
