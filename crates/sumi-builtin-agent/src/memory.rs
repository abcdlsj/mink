use std::{
    path::{Component, Path, PathBuf},
    time::UNIX_EPOCH,
};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::{agent::AgentError, workspace::validate_relative_path};

pub const PRIMARY_MEMORY_PATH: &str = "MEMORY.md";

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct MemoryFile {
    pub path: String,
    pub size: u64,
    pub sha256: String,
    pub modified_at_unix: u64,
}

pub(crate) async fn list_memory(root: &Path) -> Result<Vec<MemoryFile>, AgentError> {
    let canonical_root = tokio::fs::canonicalize(root)
        .await
        .map_err(|_| AgentError::NotFound)?;
    let mut files = Vec::new();
    let mut pending = vec![canonical_root.clone()];
    while let Some(directory) = pending.pop() {
        let mut entries = tokio::fs::read_dir(&directory)
            .await
            .map_err(|_| AgentError::Internal)?;
        while let Some(entry) = entries
            .next_entry()
            .await
            .map_err(|_| AgentError::Internal)?
        {
            let path = entry.path();
            let metadata = tokio::fs::symlink_metadata(&path)
                .await
                .map_err(|_| AgentError::Internal)?;
            if metadata.file_type().is_symlink() {
                continue;
            }
            if metadata.is_dir() {
                pending.push(path);
                continue;
            }
            if !metadata.file_type().is_file() {
                continue;
            }
            let Ok(relative) = path.strip_prefix(&canonical_root) else {
                continue;
            };
            let Some(relative) = relative.to_str() else {
                continue;
            };
            let content = tokio::fs::read(&path)
                .await
                .map_err(|_| AgentError::Internal)?;
            files.push(MemoryFile {
                path: relative.to_owned(),
                size: content.len() as u64,
                sha256: hex::encode(Sha256::digest(&content)),
                modified_at_unix: metadata
                    .modified()
                    .ok()
                    .and_then(|modified| modified.duration_since(UNIX_EPOCH).ok())
                    .map(|duration| duration.as_secs())
                    .unwrap_or(0),
            });
        }
    }
    files.sort_by(|left, right| left.path.cmp(&right.path));
    Ok(files)
}

pub(crate) async fn read_memory(root: &Path, path: &str) -> Result<Vec<u8>, AgentError> {
    let relative = validate_rooted_path(path)?;
    let target = resolve_existing_file(root, &relative).await?;
    tokio::fs::read(&target)
        .await
        .map_err(|_| AgentError::Internal)
}

pub(crate) async fn write_memory(
    root: &Path,
    path: &str,
    content: &[u8],
) -> Result<(), AgentError> {
    let relative = validate_rooted_path(path)?;
    let target = resolve_write_file(root, &relative).await?;
    tokio::fs::write(&target, content)
        .await
        .map_err(|_| AgentError::Internal)
}

fn validate_rooted_path(value: &str) -> Result<PathBuf, AgentError> {
    let path = Path::new(value);
    validate_relative_path(path).map_err(|_| AgentError::Conflict)?;
    Ok(path.to_owned())
}

async fn resolve_existing_file(root: &Path, relative: &Path) -> Result<PathBuf, AgentError> {
    let canonical_root = tokio::fs::canonicalize(root)
        .await
        .map_err(|_| AgentError::NotFound)?;
    let mut candidate = root.to_owned();
    for component in relative.components() {
        let Component::Normal(component) = component else {
            return Err(AgentError::Conflict);
        };
        candidate.push(component);
        let metadata = tokio::fs::symlink_metadata(&candidate)
            .await
            .map_err(|_| AgentError::NotFound)?;
        if metadata.file_type().is_symlink() {
            return Err(AgentError::Conflict);
        }
    }
    let canonical = tokio::fs::canonicalize(&candidate)
        .await
        .map_err(|_| AgentError::NotFound)?;
    if !canonical.starts_with(&canonical_root) {
        return Err(AgentError::Conflict);
    }
    if !tokio::fs::metadata(&canonical)
        .await
        .map_err(|_| AgentError::Internal)?
        .is_file()
    {
        return Err(AgentError::Conflict);
    }
    Ok(canonical)
}

async fn resolve_write_file(root: &Path, relative: &Path) -> Result<PathBuf, AgentError> {
    let canonical_root = tokio::fs::canonicalize(root)
        .await
        .map_err(|_| AgentError::NotFound)?;
    let components = relative.components().collect::<Vec<_>>();
    let mut parent = root.to_owned();
    for component in &components[..components.len() - 1] {
        let Component::Normal(component) = component else {
            return Err(AgentError::Conflict);
        };
        parent.push(component);
        match tokio::fs::symlink_metadata(&parent).await {
            Ok(metadata) => {
                if metadata.file_type().is_symlink() {
                    return Err(AgentError::Conflict);
                }
                if !metadata.is_dir() {
                    return Err(AgentError::Conflict);
                }
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                tokio::fs::create_dir(&parent)
                    .await
                    .map_err(|_| AgentError::Internal)?;
            }
            Err(_) => return Err(AgentError::Internal),
        }
    }
    let canonical_parent = tokio::fs::canonicalize(&parent)
        .await
        .map_err(|_| AgentError::Internal)?;
    if !canonical_parent.starts_with(&canonical_root) {
        return Err(AgentError::Conflict);
    }
    let Some(Component::Normal(file_name)) = components.last() else {
        return Err(AgentError::Conflict);
    };
    let candidate = canonical_parent.join(file_name);
    match tokio::fs::symlink_metadata(&candidate).await {
        Ok(metadata) => {
            if metadata.file_type().is_symlink() || !metadata.is_file() {
                return Err(AgentError::Conflict);
            }
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(_) => return Err(AgentError::Internal),
    }
    Ok(candidate)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn memory_round_trips_and_lists_projection() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().join("memory");
        tokio::fs::create_dir_all(root.join("notes")).await.unwrap();

        write_memory(&root, "MEMORY.md", b"# memory").await.unwrap();
        write_memory(&root, "notes/topic.md", b"note")
            .await
            .unwrap();

        assert_eq!(read_memory(&root, "MEMORY.md").await.unwrap(), b"# memory");
        let files = list_memory(&root).await.unwrap();
        assert_eq!(
            files
                .iter()
                .map(|file| file.path.as_str())
                .collect::<Vec<_>>(),
            vec!["MEMORY.md", "notes/topic.md"]
        );
        assert_eq!(files[0].size, 8);
        assert_eq!(files[0].sha256, hex::encode(Sha256::digest(b"# memory")));
    }

    #[tokio::test]
    async fn memory_rejects_escape_and_symlink_paths() {
        let directory = tempfile::tempdir().unwrap();
        let root = directory.path().join("memory");
        tokio::fs::create_dir_all(&root).await.unwrap();
        let outside = directory.path().join("secret");
        tokio::fs::write(&outside, b"hidden").await.unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(&outside, root.join("link.txt")).unwrap();

        assert_eq!(
            read_memory(&root, "../secret").await,
            Err(AgentError::Conflict)
        );
        assert_eq!(
            read_memory(&root, "/etc/passwd").await,
            Err(AgentError::Conflict)
        );
        assert_eq!(
            read_memory(&root, "missing.txt").await,
            Err(AgentError::NotFound)
        );
        #[cfg(unix)]
        assert_eq!(
            read_memory(&root, "link.txt").await,
            Err(AgentError::Conflict)
        );
        #[cfg(unix)]
        assert_eq!(
            write_memory(&root, "link.txt", b"overwrite").await,
            Err(AgentError::Conflict)
        );
    }
}
