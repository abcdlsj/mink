use std::{
    collections::HashMap,
    sync::{Arc, Mutex},
};

use tokio::sync::{mpsc, oneshot};
use uuid::Uuid;

use crate::{
    ids::QueryId,
    protocol::computer::{
        Query, QueryEnvelope, QueryErrorCode, QueryResult, QueryResultEnvelope, ServerFrame,
    },
};

const QUERY_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(2);

#[derive(Clone, Default)]
pub(super) struct QueryRegistry {
    state: Arc<Mutex<RegistryState>>,
}

#[derive(Default)]
struct RegistryState {
    next_connection: u64,
    computers: HashMap<Uuid, Connection>,
    pending: HashMap<QueryId, oneshot::Sender<QueryResult>>,
}

struct Connection {
    id: u64,
    frames: mpsc::UnboundedSender<ServerFrame>,
}

pub(super) struct ConnectionHandle {
    computer_id: Uuid,
    id: u64,
}

impl QueryRegistry {
    pub(super) fn connect(
        &self,
        computer_id: Uuid,
    ) -> (ConnectionHandle, mpsc::UnboundedReceiver<ServerFrame>) {
        let (frames, receiver) = mpsc::unbounded_channel();
        let mut state = self.state.lock().expect("query registry lock");
        state.next_connection += 1;
        let id = state.next_connection;
        state
            .computers
            .insert(computer_id, Connection { id, frames });
        (ConnectionHandle { computer_id, id }, receiver)
    }

    pub(super) fn disconnect(&self, handle: ConnectionHandle) {
        let mut state = self.state.lock().expect("query registry lock");
        // A stale disconnect must not remove a newer connection for the same Computer.
        if state
            .computers
            .get(&handle.computer_id)
            .is_some_and(|connection| connection.id == handle.id)
        {
            state.computers.remove(&handle.computer_id);
        }
    }

    pub(super) async fn ask(&self, computer_id: Uuid, query: Query) -> QueryResult {
        let query_id = QueryId::from_uuid(Uuid::now_v7());
        let (sender, receiver) = oneshot::channel();
        {
            let mut state = self.state.lock().expect("query registry lock");
            let Some(connection) = state.computers.get(&computer_id) else {
                return unreachable();
            };
            if connection
                .frames
                .send(ServerFrame::Query {
                    query: QueryEnvelope { query_id, query },
                })
                .is_err()
            {
                return unreachable();
            }
            state.pending.insert(query_id, sender);
        }
        match tokio::time::timeout(QUERY_TIMEOUT, receiver).await {
            Ok(Ok(result)) => result,
            Ok(Err(_)) | Err(_) => {
                self.state
                    .lock()
                    .expect("query registry lock")
                    .pending
                    .remove(&query_id);
                unreachable()
            }
        }
    }

    pub(super) fn resolve(&self, envelope: QueryResultEnvelope) {
        let waiting = self
            .state
            .lock()
            .expect("query registry lock")
            .pending
            .remove(&envelope.query_id);
        if let Some(waiting) = waiting {
            let _ = waiting.send(envelope.result);
        }
    }
}

fn unreachable() -> QueryResult {
    QueryResult::Unavailable {
        code: QueryErrorCode::Unreachable,
    }
}
