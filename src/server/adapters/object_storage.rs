use std::sync::Arc;

use async_trait::async_trait;
use object_store::{ObjectStore, path::Path};
use sha2::{Digest, Sha256};

use crate::server::application::ports::{ApplicationError, AttachmentObjectPort, StoredObject};

pub(super) struct AttachmentObjectStore {
    store: Arc<dyn ObjectStore>,
}

impl AttachmentObjectStore {
    pub(super) fn new(store: Arc<dyn ObjectStore>) -> Self {
        Self { store }
    }

    fn path(object_key: &str) -> Result<Path, ApplicationError> {
        if object_key.is_empty()
            || object_key.starts_with('/')
            || object_key
                .split('/')
                .any(|part| part.is_empty() || part == "." || part == "..")
        {
            return Err(ApplicationError::Conflict);
        }
        Path::parse(object_key).map_err(|_| ApplicationError::Conflict)
    }
}

#[async_trait]
impl AttachmentObjectPort for AttachmentObjectStore {
    async fn put(
        &self,
        object_key: &str,
        content: Vec<u8>,
    ) -> Result<StoredObject, ApplicationError> {
        let path = Self::path(object_key)?;
        let length = u64::try_from(content.len()).map_err(|_| ApplicationError::Conflict)?;
        let sha256 = Sha256::digest(&content).into();
        self.store
            .put(&path, content.into())
            .await
            .map_err(|_| ApplicationError::Unavailable)?;
        Ok(StoredObject { length, sha256 })
    }

    async fn get(&self, object_key: &str) -> Result<Vec<u8>, ApplicationError> {
        let path = Self::path(object_key)?;
        let result = self.store.get(&path).await.map_err(|error| match error {
            object_store::Error::NotFound { .. } => ApplicationError::NotFound,
            _ => ApplicationError::Unavailable,
        })?;
        result
            .bytes()
            .await
            .map(|bytes| bytes.to_vec())
            .map_err(|_| ApplicationError::Unavailable)
    }

    async fn delete(&self, object_key: &str) -> Result<(), ApplicationError> {
        let path = Self::path(object_key)?;
        self.store.delete(&path).await.map_err(|error| match error {
            object_store::Error::NotFound { .. } => ApplicationError::NotFound,
            _ => ApplicationError::Unavailable,
        })
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use object_store::local::LocalFileSystem;
    use sha2::{Digest, Sha256};

    use super::*;

    #[tokio::test]
    async fn object_store_rejects_escaping_keys_and_reports_verified_content() {
        let directory = tempfile::tempdir().unwrap();
        let local = LocalFileSystem::new_with_prefix(directory.path()).unwrap();
        let adapter = AttachmentObjectStore::new(Arc::new(local));

        let content = b"attachment body".to_vec();
        let stored = adapter
            .put("spaces/one/attachment", content.clone())
            .await
            .unwrap();
        assert_eq!(stored.length, content.len() as u64);
        assert_eq!(stored.sha256, <[u8; 32]>::from(Sha256::digest(&content)));
        assert_eq!(adapter.get("spaces/one/attachment").await.unwrap(), content);
        assert_eq!(
            adapter
                .put("spaces/../secret", Vec::new())
                .await
                .unwrap_err(),
            ApplicationError::Conflict
        );
    }
}
