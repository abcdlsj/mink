use time::OffsetDateTime;

use crate::ids::{ComputerId, IdempotencyKey, MemberId, SpaceId};

use crate::server::domain::pairing::{Pairing, PairingRequest, PairingStatus};

use super::ports::{
    ApplicationError, ComputerRecord, EffectSink, IdentityTransaction, PairedComputer,
    PairingCodePort, PairingView, ServerTransaction, TransactionPort,
};

pub(in crate::server) struct BeginPairing;

pub(in crate::server) struct BeginPairingInput<'a> {
    pub(in crate::server) pairing_id: uuid::Uuid,
    pub(in crate::server) token_hash: &'a str,
    pub(in crate::server) hostname: &'a str,
    pub(in crate::server) os: &'a str,
    pub(in crate::server) daemon_version: &'a str,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct StartedPairing {
    pub(in crate::server) pairing_id: uuid::Uuid,
    pub(in crate::server) code: String,
    pub(in crate::server) expires_at: OffsetDateTime,
}

impl BeginPairing {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        codes: &impl PairingCodePort,
        input: BeginPairingInput<'_>,
    ) -> Result<StartedPairing, ApplicationError> {
        let request = PairingRequest::new(
            input.token_hash,
            input.hostname,
            input.os,
            input.daemon_version,
        )?;
        let pairing = Pairing::open(request, input.now);
        let code = codes.generate();
        let code_hash = code.sha256_hash();
        let expires_at = pairing.expires_at;
        port.transact(async |transaction| {
            transaction
                .insert_pairing(input.pairing_id, &pairing, &code_hash, input.now)
                .await
        })
        .await?;
        Ok(StartedPairing {
            pairing_id: input.pairing_id,
            code: code.expose().to_owned(),
            expires_at,
        })
    }
}

pub(in crate::server) struct ReadPairing;

impl ReadPairing {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        pairing_id: uuid::Uuid,
        code_hash: &str,
        now: OffsetDateTime,
    ) -> Result<PairingView, ApplicationError> {
        port.transact(async |transaction| {
            let mut pairing = transaction
                .pairing_by_code(pairing_id, code_hash)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            lapse_if_needed(transaction, pairing_id, &mut pairing, now).await?;
            Ok(PairingView {
                pairing_id,
                hostname: pairing.request.hostname.clone(),
                os: pairing.request.os,
                daemon_version: pairing.request.daemon_version.clone(),
                token_fingerprint: pairing.token_fingerprint().to_owned(),
                status: pairing.status,
                expires_at: pairing.expires_at,
            })
        })
        .await
    }
}

pub(in crate::server) struct ReadPairingStatus;

pub(in crate::server) struct PairingProgress {
    pub(in crate::server) status: PairingStatus,
    pub(in crate::server) computer_id: Option<ComputerId>,
    pub(in crate::server) space_id: Option<SpaceId>,
}

impl ReadPairingStatus {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        pairing_id: uuid::Uuid,
        token_hash: &str,
        now: OffsetDateTime,
    ) -> Result<PairingProgress, ApplicationError> {
        port.transact(async |transaction| {
            let mut pairing = transaction
                .pairing_by_token(pairing_id, token_hash)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            lapse_if_needed(transaction, pairing_id, &mut pairing, now).await?;
            Ok(PairingProgress {
                status: pairing.status,
                computer_id: pairing.computer_id,
                space_id: pairing.space_id,
            })
        })
        .await
    }
}

pub(in crate::server) struct ConfirmPairing;

pub(in crate::server) struct ConfirmPairingInput<'a> {
    pub(in crate::server) actor_id: MemberId,
    pub(in crate::server) pairing_id: uuid::Uuid,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) code_hash: &'a str,
    pub(in crate::server) name: &'a str,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

const CONFIRM_ACTION: &str = "computer.pairing.confirm";

impl ConfirmPairing {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: ConfirmPairingInput<'_>,
    ) -> Result<PairedComputer, ApplicationError> {
        let name = input.name.trim();
        if name.is_empty() {
            return Err(crate::server::domain::DomainError::InvalidPairing.into());
        }
        port.transact(async |transaction| {
            transaction
                .lock_idempotency(input.actor_id, CONFIRM_ACTION, input.idempotency_key)
                .await?;
            if let Some(computer_id) = transaction
                .resource_for_idempotency(input.actor_id, CONFIRM_ACTION, input.idempotency_key)
                .await?
            {
                return transaction
                    .paired_computer(ComputerId::from_uuid(computer_id))
                    .await?
                    .ok_or(ApplicationError::NotFound);
            }
            let mut pairing = transaction
                .pairing_by_code_for_update(input.pairing_id, input.code_hash)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            if pairing.has_lapsed(input.now) {
                pairing.lapse();
                transaction
                    .save_pairing(input.pairing_id, &pairing, input.now)
                    .await?;
            }
            pairing.confirm(input.computer_id, input.space_id, input.now)?;
            let record = ComputerRecord {
                id: input.computer_id,
                space_id: input.space_id,
                name: name.to_owned(),
                hostname: pairing.request.hostname.clone(),
                os: pairing.request.os,
                daemon_version: pairing.request.daemon_version.clone(),
                token_hash: pairing.request.token_hash.clone(),
                created_at: input.now,
            };
            transaction.insert_computer(&record).await?;
            transaction
                .save_pairing(input.pairing_id, &pairing, input.now)
                .await?;
            transaction
                .record_resource_idempotency(
                    input.actor_id,
                    CONFIRM_ACTION,
                    input.idempotency_key,
                    input.computer_id.into_uuid(),
                )
                .await?;
            Ok(PairedComputer {
                id: record.id,
                space_id: record.space_id,
                name: record.name,
                hostname: record.hostname,
                os: record.os,
                daemon_version: Some(record.daemon_version),
                connected: false,
                deleted: false,
                last_seen_at: None,
                created_at: record.created_at,
            })
        })
        .await
    }
}

async fn lapse_if_needed<T: ServerTransaction>(
    transaction: &mut T,
    pairing_id: uuid::Uuid,
    pairing: &mut Pairing,
    now: OffsetDateTime,
) -> Result<(), ApplicationError> {
    if pairing.has_lapsed(now) {
        pairing.lapse();
        transaction.save_pairing(pairing_id, pairing, now).await?;
    }
    Ok(())
}

pub(in crate::server) struct ReadPairedComputer;

impl ReadPairedComputer {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        computer_id: ComputerId,
    ) -> Result<PairedComputer, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .paired_computer(computer_id)
                .await?
                .ok_or(ApplicationError::NotFound)
        })
        .await
    }
}

pub(in crate::server) struct ListSpaceComputers;

impl ListSpaceComputers {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        space_id: SpaceId,
    ) -> Result<Vec<PairedComputer>, ApplicationError> {
        port.transact(async |transaction| transaction.space_computers(space_id).await)
            .await
    }
}

pub(in crate::server) struct AuthenticateComputer;

pub(in crate::server) struct ComputerIdentity {
    pub(in crate::server) deleted: bool,
}

impl AuthenticateComputer {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<ComputerIdentity, ApplicationError> {
        port.transact(async |transaction| {
            let deleted = transaction
                .computer_for_token(computer_id, token_hash)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            Ok(ComputerIdentity { deleted })
        })
        .await
    }

    pub(in crate::server) async fn require_active<P: TransactionPort>(
        port: &mut P,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<ComputerIdentity, ApplicationError> {
        let identity = Self::execute(port, computer_id, token_hash).await?;
        if identity.deleted {
            return Err(ApplicationError::Unauthenticated);
        }
        Ok(identity)
    }
}
