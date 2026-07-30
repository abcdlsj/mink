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

/// Computer 回应 query 的等待上限。超时与离线返回同一个结果:调用方可用的事实相同。
const QUERY_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(2);

/// 在线 Computer 的出站通道与未完成 query 的登记表。query 不持久化,进程重启或
/// 连接断开后登记表随之失效,调用方重新发起。
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

/// 连接登记凭证。`id` 用于区分同一个 Computer 的先后两次连接,断开时只清理自己
/// 那次,避免关掉已经重连上来的通道。
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
        if state
            .computers
            .get(&handle.computer_id)
            .is_some_and(|connection| connection.id == handle.id)
        {
            state.computers.remove(&handle.computer_id);
        }
    }

    /// 向 Computer 取值。Computer 未连接或未在超时内回应时返回 `unreachable`。
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
