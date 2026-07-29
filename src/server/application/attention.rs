use time::OffsetDateTime;

use crate::ids::{InboxItemId, RunId};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) struct AttachHardItemInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) item_id: InboxItemId,
    pub(in crate::server) lease_expires_at: OffsetDateTime,
}

pub(in crate::server) struct AttachHardItem;

impl AttachHardItem {
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: AttachHardItemInput,
    ) -> Result<u64, ApplicationError> {
        port.transact(|transaction| {
            let mut run = transaction.run(input.run_id)?;
            let mut item = transaction.inbox_item(input.item_id)?;
            if let Some(existing) = run
                .items
                .iter()
                .find(|existing| existing.inbox_item_id == item.id)
            {
                if item.lease_run_id == Some(run.id) {
                    return Ok(existing.delivery_sequence);
                }
                return Err(ApplicationError::ContextChanged);
            }
            let sequence = run.attach(&item)?;
            item.lease(run.id, input.lease_expires_at)?;
            transaction.save_run(run.clone())?;
            transaction.save_inbox_item(item)?;
            transaction.emit(Effect::ItemAttached {
                run_id: run.id,
                item_id: input.item_id,
                sequence,
            });
            Ok(sequence)
        })
    }
}
