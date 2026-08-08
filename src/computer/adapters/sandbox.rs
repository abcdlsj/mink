use std::path::Path;
use std::path::PathBuf;

use tokio::process::Command;

use crate::computer::application::ApplicationError;

pub(in crate::computer) struct SandboxAdapter;

impl SandboxAdapter {
    /// The shell used inside the sandbox. macOS ships bash 3.2 whose here-doc and here-string
    /// temporary files ignore `TMPDIR` and always go to `/tmp`; a Homebrew bash honors `TMPDIR`,
    /// so it is preferred on macOS and keeps heredoc scratch inside the Agent's `runs/` directory.
    /// The sandbox still allows `/tmp` writes, so the system shell works even without Homebrew bash.
    #[cfg(target_os = "macos")]
    pub(in crate::computer) fn shell() -> PathBuf {
        for candidate in ["/opt/homebrew/bin/bash", "/usr/local/bin/bash"] {
            let path = PathBuf::from(candidate);
            if path.is_file() {
                return path;
            }
        }
        PathBuf::from("/bin/sh")
    }

    #[cfg(not(target_os = "macos"))]
    pub(in crate::computer) fn shell() -> PathBuf {
        PathBuf::from("/bin/sh")
    }

    pub(in crate::computer) fn validate() -> Result<(), ApplicationError> {
        #[cfg(target_os = "macos")]
        if Path::new("/usr/bin/sandbox-exec").is_file() {
            return Ok(());
        }
        #[cfg(target_os = "linux")]
        if executable_on_path("bwrap").is_some() {
            return Ok(());
        }
        Err(ApplicationError::DriverUnavailable)
    }

    pub(in crate::computer) fn command(
        executable: &Path,
        agent_home: &Path,
        driver_home: &Path,
        socket: &Path,
        driver_token: &str,
    ) -> Result<Command, ApplicationError> {
        Self::validate()?;
        #[cfg(target_os = "macos")]
        {
            let sumi_executable = std::env::current_exe().unwrap_or_else(|_| executable.to_owned());
            // macOS sandbox profiles match the resolved filesystem path. Paths like /tmp are
            // symlinks to /private/tmp, so profile entries built from the unresolved path silently
            // fail to grant read or write access.
            let executable = canonicalize_or_original(executable);
            let sumi_executable = canonicalize_or_original(&sumi_executable);
            let agent_home = canonicalize_or_original(agent_home);
            let driver_home = canonicalize_or_original(driver_home);
            let socket = canonicalize_or_original(socket);
            let profile = format!(
                "(version 1)(deny default)(allow process*)(allow network-outbound)\
                 (allow file-read* (subpath \"/System\") (subpath \"/usr\") \
                  (subpath \"/bin\") (subpath \"/sbin\") (subpath \"/Library\") \
                  (subpath \"/private\") (literal \"/\") (literal \"/dev/null\") \
                  (literal \"/dev/urandom\") \
                  (literal \"{}\") (literal \"{}\") (literal \"{}\") \
                  (subpath \"{}\") (subpath \"{}\") (subpath \"{}\") \
                  (literal \"{}\"))\
                 (allow file-read-metadata)\
                 (allow sysctl-read)\
                 (allow file-write* (subpath \"{}\") (subpath \"{}\") \
                  (subpath \"/private/tmp\") (subpath \"/private/var/tmp\") \
                  (literal \"{}\") (literal \"/dev/null\"))",
                escape(&executable)?,
                escape(&sumi_executable)?,
                escape(&agent_home)?,
                escape(&agent_home.join("workspace"))?,
                escape(&agent_home.join("memory"))?,
                escape(&agent_home.join("runs"))?,
                escape(&socket)?,
                escape(&agent_home.join("workspace"))?,
                escape(&agent_home.join("runs"))?,
                escape(&socket)?,
            );
            let mut command = Command::new("/usr/bin/sandbox-exec");
            command.arg("-p").arg(profile).arg(executable);
            configure_environment(
                &mut command,
                &agent_home,
                &driver_home,
                &socket,
                driver_token,
            );
            return Ok(command);
        }
        #[cfg(target_os = "linux")]
        {
            let bwrap = executable_on_path("bwrap").ok_or(ApplicationError::DriverUnavailable)?;
            let mut command = Command::new(bwrap);
            command
                .arg("--die-with-parent")
                .arg("--unshare-all")
                .arg("--share-net")
                .arg("--proc")
                .arg("/proc")
                .arg("--dev")
                .arg("/dev")
                .arg("--ro-bind")
                .arg("/usr")
                .arg("/usr")
                .arg("--ro-bind")
                .arg("/lib")
                .arg("/lib");
            if let Ok(current_executable) = std::env::current_exe()
                && let Some(parent) = current_executable.parent()
            {
                command.arg("--ro-bind").arg(parent).arg(parent);
            }
            command
                .arg("--dir")
                .arg("/agent")
                .arg("--dir")
                .arg("/runtime")
                .arg("--tmpfs")
                .arg("/tmp")
                .arg("--bind")
                .arg(agent_home.join("workspace"))
                .arg("/agent/workspace")
                .arg("--bind")
                .arg(agent_home.join("memory"))
                .arg("/agent/memory")
                .arg("--bind")
                .arg(agent_home.join("runs"))
                .arg("/agent/runs")
                .arg("--ro-bind")
                .arg(driver_home)
                .arg("/agent/driver")
                .arg("--ro-bind")
                .arg(socket)
                .arg("/runtime/daemon.sock")
                .arg("--")
                .arg(executable);
            configure_environment(
                &mut command,
                Path::new("/agent"),
                Path::new("/agent/driver"),
                Path::new("/runtime/daemon.sock"),
                driver_token,
            );
            return Ok(command);
        }
        #[allow(unreachable_code)]
        Err(ApplicationError::DriverUnavailable)
    }
}

#[cfg(target_os = "macos")]
fn canonicalize_or_original(path: &Path) -> PathBuf {
    std::fs::canonicalize(path).unwrap_or_else(|_| path.to_owned())
}

fn configure_environment(
    command: &mut Command,
    agent_home: &Path,
    driver_home: &Path,
    socket: &Path,
    driver_token: &str,
) {
    let mut path_entries = Vec::new();
    if let Ok(current_executable) = std::env::current_exe()
        && let Some(parent) = current_executable.parent()
    {
        path_entries.push(parent.to_owned());
    }
    for entry in ["/usr/local/bin", "/usr/bin", "/bin", "/opt/homebrew/bin"] {
        path_entries.push(PathBuf::from(entry));
    }
    let path = std::env::join_paths(path_entries)
        .unwrap_or_else(|_| "/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin".into());
    command
        .env_clear()
        .env("PATH", path)
        .env("HOME", agent_home)
        .env("CODEX_HOME", driver_home)
        // Heredocs and mktemp write through TMPDIR; on macOS the sandbox profile
        // grants writes to the canonical path, so the env var must be canonical too.
        .env(
            "TMPDIR",
            std::fs::canonicalize(agent_home.join("runs"))
                .unwrap_or_else(|_| agent_home.join("runs")),
        )
        .env("SUMI_SOCKET", socket)
        .env("SUMI_DRIVER_TOKEN", driver_token)
        // Start at Agent Home so shell paths match the file-tool contract
        // (`workspace/<path>`, `memory/<path>`) instead of duplicating the prefix.
        .current_dir(agent_home);
}

#[cfg(target_os = "linux")]
fn executable_on_path(name: &str) -> Option<PathBuf> {
    std::env::var_os("PATH").and_then(|path| {
        std::env::split_paths(&path)
            .map(|directory| directory.join(name))
            .find(|candidate| candidate.is_file())
    })
}

#[cfg(target_os = "macos")]
fn escape(path: &Path) -> Result<String, ApplicationError> {
    path.to_str()
        .map(|value| value.replace('\\', "\\\\").replace('"', "\\\""))
        .ok_or(ApplicationError::Internal)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn shell_starts_at_agent_home_root() {
        if SandboxAdapter::validate().is_err() {
            return;
        }
        let home = tempfile::tempdir().unwrap();
        let socket = home.path().join("runtime/daemon.sock");
        let mut command = SandboxAdapter::command(
            &SandboxAdapter::shell(),
            home.path(),
            &home.path().join("drivers/builtin"),
            &socket,
            "test-token",
        )
        .unwrap();
        let output = command.arg("-c").arg("pwd -P").output().await.unwrap();
        assert!(output.status.success());
        let expected = if cfg!(target_os = "linux") {
            PathBuf::from("/agent")
        } else {
            std::fs::canonicalize(home.path()).unwrap()
        };
        assert_eq!(
            String::from_utf8_lossy(&output.stdout).trim(),
            expected.to_str().unwrap()
        );
    }
}
