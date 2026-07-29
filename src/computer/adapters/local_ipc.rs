use std::path::{Path, PathBuf};

use serde::{Serialize, de::DeserializeOwned};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};

use crate::computer::application::ApplicationError;

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
}
