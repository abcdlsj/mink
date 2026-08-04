use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use tokio::sync::mpsc;
use uuid::Uuid;

/// In-process wake-up registry for queued Computer commands.
///
/// The Server persists commands in PostgreSQL, then pushes them over the Computer WebSocket. This
/// registry lets the transaction that committed a command wake the online connection immediately
/// instead of waiting for the next heartbeat. It is a delivery optimization only: the database
/// remains the source of truth, and reconnect/heartbeat replay still guarantees delivery.
#[derive(Clone, Default)]
pub(super) struct CommandRegistry {
    state: Arc<Mutex<RegistryState>>,
}

#[derive(Default)]
struct RegistryState {
    next_connection: u64,
    computers: HashMap<Uuid, Connection>,
}

struct Connection {
    id: u64,
    wakeups: mpsc::UnboundedSender<()>,
}

pub(super) struct CommandConnection {
    computer_id: Uuid,
    id: u64,
}

impl CommandRegistry {
    pub(super) fn connect(
        &self,
        computer_id: Uuid,
    ) -> (CommandConnection, mpsc::UnboundedReceiver<()>) {
        let (wakeups, receiver) = mpsc::unbounded_channel();
        let mut state = self.state.lock().expect("command registry lock");
        state.next_connection += 1;
        let id = state.next_connection;
        state
            .computers
            .insert(computer_id, Connection { id, wakeups });
        (CommandConnection { computer_id, id }, receiver)
    }

    pub(super) fn disconnect(&self, connection: CommandConnection) {
        let mut state = self.state.lock().expect("command registry lock");
        // A stale disconnect must not remove a newer connection for the same Computer.
        if state
            .computers
            .get(&connection.computer_id)
            .is_some_and(|existing| existing.id == connection.id)
        {
            state.computers.remove(&connection.computer_id);
        }
    }

    pub(super) fn notify(&self, computer_id: Uuid) {
        let sender = self
            .state
            .lock()
            .expect("command registry lock")
            .computers
            .get(&computer_id)
            .map(|connection| connection.wakeups.clone());
        if let Some(sender) = sender {
            let _ = sender.send(());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn notify_wakes_only_the_connected_computer() {
        let registry = CommandRegistry::default();
        let computer = Uuid::now_v7();
        let (_connection, mut receiver) = registry.connect(computer);

        registry.notify(computer);
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(100), receiver.recv())
                .await
                .is_ok_and(|wakeup| wakeup == Some(()))
        );

        registry.notify(Uuid::now_v7());
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(50), receiver.recv())
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn stale_disconnect_keeps_the_newest_connection() {
        let registry = CommandRegistry::default();
        let computer = Uuid::now_v7();
        let (old_connection, mut old_receiver) = registry.connect(computer);
        let (_new_connection, mut new_receiver) = registry.connect(computer);

        registry.disconnect(old_connection);
        registry.notify(computer);

        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(100), old_receiver.recv())
                .await
                .is_ok_and(|wakeup| wakeup.is_none())
        );
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(100), new_receiver.recv())
                .await
                .is_ok_and(|wakeup| wakeup == Some(()))
        );
    }
}
