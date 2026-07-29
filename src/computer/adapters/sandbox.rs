use std::path::{Path, PathBuf};

use tokio::process::Command;

use crate::computer::application::ApplicationError;

pub(in crate::computer) struct SandboxAdapter;

impl SandboxAdapter {
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
        run_token: &str,
    ) -> Result<Command, ApplicationError> {
        Self::validate()?;
        #[cfg(target_os = "macos")]
        {
            let profile = format!(
                "(version 1)(deny default)(allow process*)(allow network-outbound)\
                 (allow file-read* (subpath \"/System\") (subpath \"/usr\") \
                  (subpath \"/bin\") (subpath \"/sbin\") (subpath \"/Library\") \
                  (literal \"{}\") (subpath \"{}\"))\
                 (allow file-write* (subpath \"{}\") (subpath \"{}\") \
                  (literal \"{}\"))",
                escape(executable)?,
                escape(agent_home)?,
                escape(&agent_home.join("workspace"))?,
                escape(&agent_home.join("runs"))?,
                escape(socket)?,
            );
            let mut command = Command::new("/usr/bin/sandbox-exec");
            command.arg("-p").arg(profile).arg(executable);
            configure_environment(&mut command, agent_home, driver_home, socket, run_token);
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
                .arg("/lib")
                .arg("--bind")
                .arg(agent_home)
                .arg("/agent")
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
                run_token,
            );
            return Ok(command);
        }
        #[allow(unreachable_code)]
        Err(ApplicationError::DriverUnavailable)
    }
}

fn configure_environment(
    command: &mut Command,
    agent_home: &Path,
    driver_home: &Path,
    socket: &Path,
    run_token: &str,
) {
    command
        .env_clear()
        .env("PATH", "/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin")
        .env("HOME", agent_home)
        .env("CODEX_HOME", driver_home)
        .env("TMPDIR", agent_home.join("runs"))
        .env("SUMI_SOCKET", socket)
        .env("SUMI_RUN_TOKEN", run_token)
        .current_dir(agent_home.join("workspace"));
}

#[cfg(target_os = "linux")]
fn executable_on_path(name: &str) -> Option<PathBuf> {
    std::env::var_os("PATH").and_then(|path| {
        std::env::split_paths(&path)
            .map(|directory| directory.join(name))
            .find(|candidate| candidate.is_file())
    })
}

#[cfg(not(target_os = "linux"))]
fn executable_on_path(_: &str) -> Option<PathBuf> {
    None
}

#[cfg(target_os = "macos")]
fn escape(path: &Path) -> Result<String, ApplicationError> {
    path.to_str()
        .map(|value| value.replace('\\', "\\\\").replace('"', "\\\""))
        .ok_or(ApplicationError::Internal)
}
