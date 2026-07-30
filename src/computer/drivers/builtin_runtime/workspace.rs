use std::{
    io::ErrorKind,
    path::{Component, Path, PathBuf},
    time::Duration,
};

use anyhow::{Context, Result, bail, ensure};
use tokio::io::{AsyncRead, AsyncReadExt};

const MAX_TOOL_FILE_BYTES: u64 = 1024 * 1024;

pub(super) async fn read_utf8(root: &Path, relative: &Path) -> Result<String> {
    let path = resolve_existing_file(root, relative).await?;
    let metadata = tokio::fs::metadata(&path).await?;
    ensure!(
        metadata.len() <= MAX_TOOL_FILE_BYTES,
        "file exceeds the 1 MiB tool limit"
    );
    tokio::fs::read_to_string(path)
        .await
        .context("file is not readable UTF-8")
}

pub(super) async fn write_utf8(root: &Path, relative: &Path, content: &str) -> Result<()> {
    ensure!(
        content.len() as u64 <= MAX_TOOL_FILE_BYTES,
        "content exceeds the 1 MiB tool limit"
    );
    let path = resolve_write_file(root, relative).await?;
    tokio::fs::write(path, content).await?;
    Ok(())
}

pub(super) async fn edit_utf8(
    root: &Path,
    relative: &Path,
    old_text: &str,
    new_text: &str,
) -> Result<()> {
    ensure!(!old_text.is_empty(), "old_text cannot be empty");
    let path = resolve_existing_file(root, relative).await?;
    let content = read_utf8(root, relative).await?;
    let Some(position) = content.find(old_text) else {
        bail!("old_text was not found");
    };
    let new_len = content.len() - old_text.len() + new_text.len();
    ensure!(
        new_len as u64 <= MAX_TOOL_FILE_BYTES,
        "edited content exceeds the 1 MiB tool limit"
    );
    let mut updated = String::with_capacity(new_len);
    updated.push_str(&content[..position]);
    updated.push_str(new_text);
    updated.push_str(&content[position + old_text.len()..]);
    tokio::fs::write(path, updated).await?;
    Ok(())
}

pub(super) fn agent_rooted_path(agent_home: &Path, value: &str) -> Result<(PathBuf, PathBuf)> {
    let relative = validated_relative(value)?;
    let mut components = relative.components();
    let Some(Component::Normal(scope)) = components.next() else {
        bail!("path must start with workspace/ or memory/");
    };
    ensure!(
        scope == "workspace" || scope == "memory",
        "path must start with workspace/ or memory/"
    );
    let remainder = components.collect::<PathBuf>();
    ensure!(!remainder.as_os_str().is_empty(), "path has no file name");
    Ok((agent_home.join(scope), remainder))
}

fn validated_relative(value: &str) -> Result<PathBuf> {
    let path = Path::new(value);
    ensure!(
        !value.is_empty() && !path.is_absolute(),
        "path must be relative"
    );
    ensure!(
        path.components()
            .all(|component| matches!(component, Component::Normal(_))),
        "path cannot contain '.', '..', a root, or a platform prefix"
    );
    Ok(path.to_owned())
}

pub(super) async fn collect_shell_output(
    mut child: tokio::process::Child,
    timeout: Duration,
    max_output_bytes: usize,
) -> Result<(std::process::ExitStatus, String)> {
    let stdout = child
        .stdout
        .take()
        .context("shell stdout was not captured")?;
    let stderr = child
        .stderr
        .take()
        .context("shell stderr was not captured")?;
    let stdout_task = tokio::spawn(read_capped(stdout, max_output_bytes));
    let stderr_task = tokio::spawn(read_capped(stderr, max_output_bytes));
    let mut process_group = ProcessGroupGuard::new(child.id());
    let status = match tokio::time::timeout(timeout, child.wait()).await {
        Ok(status) => status.context("shell command failed")?,
        Err(_) => {
            process_group.kill();
            let _ = child.wait().await;
            let _ = stdout_task.await;
            let _ = stderr_task.await;
            bail!("shell command timed out");
        }
    };
    process_group.disarm();
    let (stdout, stdout_truncated) = stdout_task.await.context("shell stdout reader failed")??;
    let (stderr, stderr_truncated) = stderr_task.await.context("shell stderr reader failed")??;

    let mut text = String::from_utf8_lossy(&stdout).into_owned();
    if !stderr.is_empty() {
        text.push_str("\nstderr:\n");
        text.push_str(&String::from_utf8_lossy(&stderr));
    }
    let combined_truncated = text.len() > max_output_bytes;
    truncate_output(&mut text, max_output_bytes);
    if (stdout_truncated || stderr_truncated) && !combined_truncated {
        text.push_str("\n[output truncated]");
    }
    Ok((status, text))
}

async fn read_capped(
    mut reader: impl AsyncRead + Unpin,
    max_bytes: usize,
) -> std::io::Result<(Vec<u8>, bool)> {
    let mut retained = Vec::with_capacity(max_bytes.min(8192));
    let mut buffer = [0_u8; 8192];
    let mut truncated = false;
    loop {
        let read = reader.read(&mut buffer).await?;
        if read == 0 {
            break;
        }
        let remaining = max_bytes.saturating_sub(retained.len());
        retained.extend_from_slice(&buffer[..read.min(remaining)]);
        truncated |= read > remaining;
    }
    Ok((retained, truncated))
}

fn truncate_output(text: &mut String, max_bytes: usize) {
    if text.len() <= max_bytes {
        return;
    }
    let mut boundary = max_bytes;
    while !text.is_char_boundary(boundary) {
        boundary -= 1;
    }
    text.truncate(boundary);
    text.push_str("\n[output truncated]");
}

struct ProcessGroupGuard {
    process_id: Option<u32>,
}

impl ProcessGroupGuard {
    fn new(process_id: Option<u32>) -> Self {
        Self { process_id }
    }

    fn disarm(&mut self) {
        self.process_id = None;
    }

    fn kill(&mut self) {
        #[cfg(unix)]
        if let Some(process_id) = self.process_id.take() {
            unsafe {
                libc::kill(-(process_id as i32), libc::SIGKILL);
            }
        }
    }
}

impl Drop for ProcessGroupGuard {
    fn drop(&mut self) {
        self.kill();
    }
}

async fn resolve_existing_file(root: &Path, relative: &Path) -> Result<PathBuf> {
    validate_relative_path(relative)?;
    let canonical_root = tokio::fs::canonicalize(root).await?;
    let mut candidate = root.to_owned();
    for component in relative.components() {
        let Component::Normal(component) = component else {
            bail!("path is invalid");
        };
        candidate.push(component);
        let metadata = tokio::fs::symlink_metadata(&candidate).await?;
        ensure!(
            !metadata.file_type().is_symlink(),
            "path cannot contain a symlink"
        );
    }
    let canonical = tokio::fs::canonicalize(&candidate).await?;
    ensure!(
        canonical.starts_with(&canonical_root),
        "path escapes its root"
    );
    ensure!(
        tokio::fs::metadata(&canonical).await?.is_file(),
        "path is not a file"
    );
    Ok(canonical)
}

async fn resolve_write_file(root: &Path, relative: &Path) -> Result<PathBuf> {
    validate_relative_path(relative)?;
    let canonical_root = tokio::fs::canonicalize(root).await?;
    let components = relative.components().collect::<Vec<_>>();
    let mut parent = root.to_owned();
    for component in &components[..components.len() - 1] {
        let Component::Normal(component) = component else {
            bail!("path is invalid");
        };
        parent.push(component);
        match tokio::fs::symlink_metadata(&parent).await {
            Ok(metadata) => {
                ensure!(
                    !metadata.file_type().is_symlink(),
                    "path cannot contain a symlink"
                );
                ensure!(metadata.is_dir(), "path parent is not a directory");
            }
            Err(error) if error.kind() == ErrorKind::NotFound => {
                tokio::fs::create_dir(&parent).await?;
            }
            Err(error) => return Err(error.into()),
        }
    }
    let canonical_parent = tokio::fs::canonicalize(&parent).await?;
    ensure!(
        canonical_parent.starts_with(&canonical_root),
        "path escapes its root"
    );
    let file_name = match components.last() {
        Some(Component::Normal(file_name)) => file_name,
        _ => bail!("path has no file name"),
    };
    let candidate = canonical_parent.join(file_name);
    match tokio::fs::symlink_metadata(&candidate).await {
        Ok(metadata) => {
            ensure!(
                !metadata.file_type().is_symlink(),
                "path cannot be a symlink"
            );
            ensure!(metadata.is_file(), "path is not a file");
        }
        Err(error) if error.kind() == ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }
    Ok(candidate)
}

fn validate_relative_path(path: &Path) -> Result<()> {
    ensure!(
        !path.as_os_str().is_empty()
            && !path.is_absolute()
            && path
                .components()
                .all(|component| matches!(component, Component::Normal(_))),
        "path is invalid"
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::process::Stdio;

    use super::*;

    #[tokio::test]
    async fn file_tools_reject_escape_and_symlink_paths() {
        let root = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        tokio::fs::write(outside.path().join("secret"), "hidden")
            .await
            .unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(outside.path(), root.path().join("link")).unwrap();

        assert!(validated_relative("../secret").is_err());
        assert!(validated_relative("/etc/passwd").is_err());
        #[cfg(unix)]
        assert!(
            read_utf8(root.path(), Path::new("link/secret"))
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn file_tools_read_write_and_edit_inside_root() {
        let root = tempfile::tempdir().unwrap();
        write_utf8(root.path(), Path::new("notes/item.md"), "before")
            .await
            .unwrap();
        edit_utf8(root.path(), Path::new("notes/item.md"), "before", "after")
            .await
            .unwrap();
        assert_eq!(
            read_utf8(root.path(), Path::new("notes/item.md"))
                .await
                .unwrap(),
            "after"
        );
    }

    #[tokio::test]
    async fn shell_output_is_drained_with_a_hard_retention_cap() {
        let mut command = tokio::process::Command::new("/bin/sh");
        command
            .args(["-c", "yes x | head -c 65536"])
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .kill_on_drop(true);
        #[cfg(unix)]
        command.process_group(0);

        let child = command.spawn().unwrap();
        let (status, output) = collect_shell_output(child, Duration::from_secs(2), 1024)
            .await
            .unwrap();

        assert!(status.success());
        assert!(output.len() <= 1024 + "\n[output truncated]".len());
        assert!(output.ends_with("[output truncated]"));
    }
}
