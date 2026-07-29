use crate::protocol::{
    computer::{
        CommandAck, CommandEnvelope, CommandSequence, ComputerHello, HandshakeErrorCode,
        ServerHandshake,
    },
    version::SUPPORTED,
};
use crate::{ids::ComputerId, server::application::ports::ApplicationError};

use super::postgres::PostgresAdapter;

pub(super) async fn replay_commands(
    storage: &PostgresAdapter,
    computer_id: ComputerId,
    watermark: CommandSequence,
) -> Result<Vec<CommandEnvelope>, ApplicationError> {
    storage.pending_commands(computer_id, watermark).await
}

pub(super) async fn acknowledge_command(
    storage: &PostgresAdapter,
    computer_id: ComputerId,
    ack: &CommandAck,
) -> Result<(), ApplicationError> {
    storage.acknowledge_command(computer_id, ack).await
}

pub(super) fn negotiate(
    hello: &ComputerHello,
    computer_deleted: bool,
    authenticated: bool,
) -> ServerHandshake {
    let rejection = if !authenticated {
        Some(HandshakeErrorCode::Unauthenticated)
    } else if computer_deleted {
        Some(HandshakeErrorCode::ComputerDeleted)
    } else if SUPPORTED.negotiate(hello.supported_versions).is_none() {
        Some(HandshakeErrorCode::NoCommonVersion)
    } else {
        None
    };
    if let Some(code) = rejection {
        return ServerHandshake::Rejected {
            code,
            supported_versions: SUPPORTED,
        };
    }
    ServerHandshake::Welcome {
        selected_version: SUPPORTED
            .negotiate(hello.supported_versions)
            .expect("common version checked above"),
        supported_versions: SUPPORTED,
        heartbeat_interval_seconds: 15,
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;

    use uuid::Uuid;

    use super::*;
    use crate::{
        ids::DaemonSessionId,
        protocol::{
            computer::{CommandSequence, DaemonCapability},
            version::{ProtocolVersion, ProtocolVersionRange},
        },
    };

    fn hello(range: ProtocolVersionRange) -> ComputerHello {
        ComputerHello {
            supported_versions: range,
            daemon_version: "1.0.0".into(),
            capabilities: BTreeSet::from([DaemonCapability::Sandbox]),
            daemon_session_id: DaemonSessionId::from_uuid(Uuid::now_v7()),
            command_watermark: CommandSequence(0),
        }
    }

    #[test]
    fn handshake_rejects_deleted_computer_and_disjoint_protocol() {
        assert!(matches!(
            negotiate(&hello(SUPPORTED), true, true),
            ServerHandshake::Rejected {
                code: HandshakeErrorCode::ComputerDeleted,
                ..
            }
        ));
        let future = ProtocolVersionRange::new(ProtocolVersion::new(2), ProtocolVersion::new(2));
        assert!(matches!(
            negotiate(&hello(future), false, true),
            ServerHandshake::Rejected {
                code: HandshakeErrorCode::NoCommonVersion,
                ..
            }
        ));
    }
}
