use super::*;

pub(in crate::server) trait TransactionPort {
    type Transaction: ServerTransaction + Send;

    async fn transact<T>(
        &mut self,
        operation: impl for<'a> AsyncFnOnce(&'a mut Self::Transaction) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError>;
}
