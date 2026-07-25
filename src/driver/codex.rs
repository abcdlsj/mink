use std::{
    path::{Path, PathBuf},
    process::Stdio,
    time::Duration,
};

use anyhow::{Context, Result, bail, ensure};
use async_trait::async_trait;
use secrecy::ExposeSecret;
use serde::Deserialize;
use tokio::{
    io::{AsyncBufReadExt, AsyncWriteExt, BufReader},
    process::Command,
};

use super::{Driver, DriverEnvironment, DriverEvent, DriverOutcome, DriverProcess, DriverRun};

pub struct CodexDriver {
    executable: PathBuf,
}

impl CodexDriver {
    pub fn new() -> Self {
        Self {
            executable: PathBuf::from("codex"),
        }
    }

    #[cfg(test)]
    pub fn with_executable(executable: PathBuf) -> Self {
        Self { executable }
    }
}

pub async fn install_sanitized_config(source: &Path, codex_home: &Path) -> Result<()> {
    let input = tokio::fs::read_to_string(source)
        .await
        .context("failed to read Codex config source")?;
    let input: toml::Table = toml::from_str(&input).context("Codex config source is invalid")?;
    let output = sanitize_config(&input)?;
    let bytes =
        toml::to_string_pretty(&output).context("failed to serialize sanitized Codex config")?;
    let path = codex_home.join("config.toml");
    tokio::fs::write(&path, bytes)
        .await
        .context("failed to write Agent Codex config")?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600)).await?;
    }
    Ok(())
}

pub async fn install_local_auth(source: &Path, codex_home: &Path) -> Result<()> {
    let auth = tokio::fs::read(source)
        .await
        .context("failed to read Codex auth source")?;
    let path = codex_home.join("auth.json");
    tokio::fs::write(&path, auth)
        .await
        .context("failed to write Agent Codex auth")?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600)).await?;
    }
    Ok(())
}

fn sanitize_config(input: &toml::Table) -> Result<toml::Table> {
    const ROOT_KEYS: &[&str] = &[
        "model_provider",
        "model",
        "model_reasoning_effort",
        "disable_response_storage",
    ];
    const PROVIDER_KEYS: &[&str] = &["name", "base_url", "wire_api", "requires_openai_auth"];

    let provider = input
        .get("model_provider")
        .and_then(toml::Value::as_str)
        .context("Codex config source has no model_provider")?;
    let mut output = toml::Table::new();
    for key in ROOT_KEYS {
        if let Some(value) = input.get(*key) {
            output.insert((*key).to_owned(), value.clone());
        }
    }
    let provider_input = input
        .get("model_providers")
        .and_then(toml::Value::as_table)
        .and_then(|providers| providers.get(provider))
        .and_then(toml::Value::as_table)
        .context("selected Codex model provider is not configured")?;
    let mut provider_output = toml::Table::new();
    for key in PROVIDER_KEYS {
        if let Some(value) = provider_input.get(*key) {
            provider_output.insert((*key).to_owned(), value.clone());
        }
    }
    ensure!(
        provider_output.contains_key("base_url"),
        "selected Codex model provider has no base_url"
    );
    let mut providers = toml::Table::new();
    providers.insert(provider.to_owned(), toml::Value::Table(provider_output));
    output.insert("model_providers".to_owned(), toml::Value::Table(providers));
    Ok(output)
}

#[derive(Deserialize)]
struct CodexEvent {
    #[serde(rename = "type")]
    kind: String,
    item: Option<CodexItem>,
}

#[derive(Deserialize)]
struct CodexItem {
    #[serde(rename = "type")]
    kind: String,
}

#[async_trait]
impl Driver for CodexDriver {
    async fn validate(&self, environment: &DriverEnvironment) -> Result<()> {
        let executable =
            resolve_executable(&self.executable).context("Codex executable is unavailable")?;
        ensure!(environment.agent_home.is_dir(), "Agent Home is unavailable");
        ensure!(
            environment.workspace.is_dir(),
            "Agent workspace is unavailable"
        );
        ensure!(
            environment.codex_home.is_dir(),
            "Agent CODEX_HOME is unavailable"
        );
        validate_sandbox_backend()?;
        let sandbox = sandboxed_command(&executable, environment)?;
        let mut command = sandbox.command;
        let status = tokio::time::timeout(
            Duration::from_secs(10),
            command
                .current_dir(&environment.workspace)
                .env_clear()
                .env("PATH", &environment.path)
                .env("HOME", &sandbox.agent_home)
                .env("CODEX_HOME", &sandbox.codex_home)
                .arg("--version")
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status(),
        )
        .await
        .context("Codex validation timed out")?
        .context("failed to validate Codex")?;
        ensure!(status.success(), "Codex validation failed");
        Ok(())
    }

    async fn start(&self, run: DriverRun) -> Result<DriverProcess> {
        self.validate(&run.environment).await?;
        let is_git_repository =
            workspace_is_git_repository(&run.environment.workspace, &run.environment.path).await;
        let executable =
            resolve_executable(&self.executable).context("Codex executable is unavailable")?;
        let sandbox = sandboxed_command(&executable, &run.environment)?;
        let mut command = sandbox.command;
        command
            .arg("exec")
            .arg("--json")
            .arg("--ephemeral")
            .arg("--color")
            .arg("never")
            .arg("--cd")
            .arg(&sandbox.workspace);
        if std::env::consts::OS == "macos" {
            command.arg("--dangerously-bypass-approvals-and-sandbox");
        } else {
            command.arg("--sandbox").arg("workspace-write");
        }
        if !is_git_repository {
            command.arg("--skip-git-repo-check");
        }
        command.arg("-");
        command
            .current_dir(&run.environment.workspace)
            .env_clear()
            .env("PATH", &run.environment.path)
            .env("HOME", &sandbox.agent_home)
            .env("CODEX_HOME", &sandbox.codex_home)
            .env("TMPDIR", sandbox.agent_home.join("runs"))
            .env("SUMI_SOCKET", &sandbox.socket_path)
            .env("SUMI_RUN_TOKEN", &run.environment.run_token)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .kill_on_drop(true);
        if let Some(api_key) = &run.environment.codex_api_key {
            command.env("CODEX_API_KEY", api_key.expose_secret());
        }
        #[cfg(unix)]
        command.process_group(0);
        let mut child = command.spawn().context("failed to start Codex Driver")?;
        let mut stdin = child.stdin.take().context("Codex stdin was not captured")?;
        stdin
            .write_all(run.prompt.as_bytes())
            .await
            .context("failed to write Codex run prompt")?;
        drop(stdin);
        let stdout = child
            .stdout
            .take()
            .context("Codex stdout was not captured")?;
        Ok(DriverProcess { child, stdout })
    }

    async fn observe(
        &self,
        process: &mut DriverProcess,
        events: &tokio::sync::mpsc::Sender<DriverEvent>,
    ) -> Result<DriverOutcome> {
        let mut lines = BufReader::new(&mut process.stdout).lines();
        let mut failed = false;
        while let Some(line) = lines.next_line().await? {
            let event: CodexEvent =
                serde_json::from_str(&line).context("Codex emitted invalid JSONL")?;
            failed |= matches!(event.kind.as_str(), "turn.failed" | "error");
            let event = normalize_event(event);
            events.send(event).await.ok();
        }
        let status = process
            .child
            .wait()
            .await
            .context("failed to wait for Codex")?;
        Ok(if status.success() && !failed {
            DriverOutcome::Completed
        } else {
            DriverOutcome::Failed
        })
    }

    async fn cancel(&self, process: &mut DriverProcess, grace_period: Duration) -> Result<()> {
        let Some(process_id) = process.child.id() else {
            return Ok(());
        };
        #[cfg(unix)]
        unsafe {
            libc::kill(-(process_id as i32), libc::SIGTERM);
        }
        if tokio::time::timeout(grace_period, process.child.wait())
            .await
            .is_err()
        {
            #[cfg(unix)]
            unsafe {
                libc::kill(-(process_id as i32), libc::SIGKILL);
            }
            process
                .child
                .wait()
                .await
                .context("failed to reap Codex after forced cancellation")?;
        }
        Ok(())
    }

    async fn cleanup(&self, _environment: &DriverEnvironment) -> Result<()> {
        Ok(())
    }
}

fn normalize_event(event: CodexEvent) -> DriverEvent {
    match event.kind.as_str() {
        "item.started"
            if event
                .item
                .as_ref()
                .is_some_and(|item| item.kind == "command_execution") =>
        {
            DriverEvent::CommandStarted
        }
        "item.completed"
            if event
                .item
                .as_ref()
                .is_some_and(|item| item.kind == "command_execution") =>
        {
            DriverEvent::CommandFinished
        }
        _ => DriverEvent::OutputReceived {
            event_type: event.kind,
        },
    }
}

fn validate_sandbox_backend() -> Result<()> {
    match std::env::consts::OS {
        "macos" => ensure!(
            Path::new("/usr/bin/sandbox-exec").is_file(),
            "sandbox-exec is unavailable"
        ),
        "linux" => ensure!(find_on_path("bwrap").is_some(), "bubblewrap is unavailable"),
        other => bail!("unsupported Driver sandbox platform: {other}"),
    }
    Ok(())
}

struct SandboxedCommand {
    command: Command,
    agent_home: PathBuf,
    workspace: PathBuf,
    codex_home: PathBuf,
    socket_path: PathBuf,
}

fn sandboxed_command(
    executable: &Path,
    environment: &DriverEnvironment,
) -> Result<SandboxedCommand> {
    let agent_home = environment.agent_home.canonicalize()?;
    let agents_root = environment.agents_root.canonicalize()?;
    let state_dir = environment.state_dir.canonicalize()?;
    ensure!(
        agent_home.starts_with(&agents_root),
        "Agent Home is outside the Agents root"
    );
    ensure!(
        agents_root.starts_with(&state_dir),
        "Agents root is outside the Computer state directory"
    );
    match std::env::consts::OS {
        "macos" => {
            let escaped_home = sandbox_string(&agent_home)?;
            let escaped_agents = sandbox_string(&agents_root)?;
            let escaped_state = sandbox_string(&state_dir)?;
            let escaped_socket = sandbox_string(&environment.socket_path)?;
            let user_home = std::env::var_os("HOME")
                .map(PathBuf::from)
                .context("HOME is unavailable")?
                .canonicalize()?;
            let escaped_user_home = sandbox_string(&user_home)?;
            let profile = format!(
                "(version 1)\n(allow default)\n(deny file-write*)\n(deny file-read* (subpath \"{escaped_user_home}\") (subpath \"{escaped_state}\"))\n(allow file-read-metadata (literal \"{escaped_state}\") (literal \"{escaped_agents}\"))\n(allow file-read* file-write* (subpath \"{escaped_home}\"))\n(allow file-read* file-write* (literal \"{escaped_socket}\"))\n"
            );
            let mut command = Command::new("/usr/bin/sandbox-exec");
            command.arg("-p").arg(profile).arg(executable);
            Ok(SandboxedCommand {
                command,
                agent_home,
                workspace: environment.workspace.canonicalize()?,
                codex_home: environment.codex_home.canonicalize()?,
                socket_path: environment.socket_path.clone(),
            })
        }
        "linux" => {
            let bubblewrap = find_on_path("bwrap").context("bubblewrap is unavailable")?;
            let sandbox_home = PathBuf::from("/sumi-agent");
            let sandbox_socket = PathBuf::from("/sumi-runtime/daemon.sock");
            let mut command = Command::new(bubblewrap);
            command.args([
                "--die-with-parent",
                "--new-session",
                "--unshare-all",
                "--share-net",
                "--tmpfs",
                "/",
            ]);
            for system_path in [
                "/usr",
                "/usr/local",
                "/opt",
                "/etc",
                "/dev",
                "/bin",
                "/sbin",
                "/lib",
                "/lib64",
            ] {
                mount_linux_runtime_path(&mut command, Path::new(system_path))?;
            }
            command
                .arg("--proc")
                .arg("/proc")
                .arg("--tmpfs")
                .arg("/tmp")
                .arg("--bind")
                .arg(&agent_home)
                .arg(&sandbox_home)
                .arg("--dir")
                .arg("/sumi-runtime")
                .arg("--ro-bind")
                .arg(&environment.socket_path)
                .arg(&sandbox_socket)
                .arg("--chdir")
                .arg(sandbox_home.join("workspace"))
                .arg(executable);
            Ok(SandboxedCommand {
                command,
                workspace: sandbox_home.join("workspace"),
                codex_home: sandbox_home.join("drivers/codex"),
                agent_home: sandbox_home,
                socket_path: sandbox_socket,
            })
        }
        other => bail!("unsupported Driver sandbox platform: {other}"),
    }
}

fn mount_linux_runtime_path(command: &mut Command, path: &Path) -> Result<()> {
    let Ok(metadata) = path.symlink_metadata() else {
        return Ok(());
    };
    if metadata.file_type().is_symlink() {
        command
            .arg("--symlink")
            .arg(std::fs::read_link(path)?)
            .arg(path);
    } else {
        command.arg("--ro-bind").arg(path).arg(path);
    }
    Ok(())
}

fn sandbox_string(path: &Path) -> Result<String> {
    let value = path.to_str().context("sandbox path is not UTF-8")?;
    Ok(value.replace('\\', "\\\\").replace('"', "\\\""))
}

fn find_on_path(name: &str) -> Option<PathBuf> {
    std::env::var_os("PATH").and_then(|paths| {
        std::env::split_paths(&paths)
            .map(|directory| directory.join(name))
            .find(|path| path.is_file())
    })
}

fn resolve_executable(executable: &Path) -> Option<PathBuf> {
    if executable.components().count() > 1 {
        executable.is_file().then(|| executable.to_owned())
    } else {
        find_on_path(executable.to_str()?)
    }
}

async fn workspace_is_git_repository(workspace: &Path, path: &str) -> bool {
    let mut command = Command::new("git");
    command
        .args(["rev-parse", "--is-inside-work-tree"])
        .current_dir(workspace)
        .env_clear()
        .env("PATH", path)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    matches!(
        tokio::time::timeout(Duration::from_secs(2), command.status()).await,
        Ok(Ok(status)) if status.success()
    )
}

#[cfg(test)]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use super::*;

    #[test]
    fn config_sanitizer_keeps_only_selected_model_provider() {
        let input: toml::Table = toml::from_str(
            r#"
model_provider = "sub2api"
model = "test-model"
model_reasoning_effort = "high"
disable_response_storage = true

[model_providers.sub2api]
name = "Sub2API"
base_url = "https://models.example.test/v1"
wire_api = "responses"
requires_openai_auth = false
http_headers = { Authorization = "secret" }

[mcp_servers.private]
headers = { Authorization = "secret" }

[hooks.state]
enabled = true
"#,
        )
        .unwrap();

        let output = sanitize_config(&input).unwrap();
        let encoded = toml::to_string(&output).unwrap();

        assert!(encoded.contains("model_provider = \"sub2api\""));
        assert!(encoded.contains("base_url = \"https://models.example.test/v1\""));
        assert!(!encoded.contains("Authorization"));
        assert!(!encoded.contains("mcp_servers"));
        assert!(!encoded.contains("hooks"));
        assert!(!encoded.contains("http_headers"));
    }

    #[tokio::test]
    async fn local_auth_is_copied_with_restricted_permissions() {
        let root = tempfile::tempdir().unwrap();
        let source = root.path().join("source-auth.json");
        let codex_home = root.path().join("agent-codex");
        std::fs::create_dir(&codex_home).unwrap();
        std::fs::write(&source, br#"{"OPENAI_API_KEY":"test-secret"}"#).unwrap();

        install_local_auth(&source, &codex_home).await.unwrap();

        let installed = codex_home.join("auth.json");
        assert_eq!(
            std::fs::read(&installed).unwrap(),
            br#"{"OPENAI_API_KEY":"test-secret"}"#
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                std::fs::metadata(installed).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
    }

    #[test]
    fn jsonl_is_normalized_without_retaining_agent_message_text() {
        let parse = |line: &str| normalize_event(serde_json::from_str::<CodexEvent>(line).unwrap());
        assert_eq!(
            parse(
                r#"{"type":"item.completed","item":{"type":"agent_message","text":"secret body"}}"#
            ),
            DriverEvent::OutputReceived {
                event_type: "item.completed".to_owned()
            }
        );
        assert_eq!(
            parse(r#"{"type":"item.started","item":{"type":"command_execution"}}"#),
            DriverEvent::CommandStarted
        );
        assert_eq!(
            parse(r#"{"type":"turn.completed"}"#),
            DriverEvent::OutputReceived {
                event_type: "turn.completed".to_owned()
            }
        );
    }

    #[tokio::test]
    async fn installed_codex_validates_inside_agent_sandbox() {
        if find_on_path("codex").is_none()
            || (std::env::consts::OS == "linux" && find_on_path("bwrap").is_none())
        {
            return;
        }
        let root = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        let home = state.join("agents/current");
        std::fs::create_dir_all(home.join("workspace")).unwrap();
        std::fs::create_dir_all(home.join("drivers/codex")).unwrap();
        let socket = state.join("daemon.sock");
        std::fs::write(&socket, "").unwrap();
        CodexDriver::new()
            .validate(&DriverEnvironment {
                state_dir: state.clone(),
                agents_root: state.join("agents"),
                agent_home: home.clone(),
                workspace: home.join("workspace"),
                codex_home: home.join("drivers/codex"),
                socket_path: socket,
                run_token: "validation-only".to_owned(),
                path: std::env::var("PATH").unwrap(),
                codex_api_key: None,
            })
            .await
            .unwrap();
    }

    #[tokio::test]
    async fn sandbox_hides_computer_state_and_passes_prompt_over_stdin() {
        if std::env::consts::OS == "linux" && find_on_path("bwrap").is_none() {
            return;
        }
        let root = tempfile::tempdir().unwrap();
        let tools = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        let current = state.join("agents/current");
        let other = state.join("agents/other");
        for path in [
            current.join("workspace"),
            current.join("drivers/codex"),
            current.join("runs"),
            other.clone(),
        ] {
            std::fs::create_dir_all(path).unwrap();
        }
        std::fs::write(state.join("secrets.json"), "computer-secret").unwrap();
        std::fs::write(other.join("private"), "other-agent-secret").unwrap();
        let socket = state.join("daemon.sock");
        std::fs::write(&socket, "").unwrap();
        let script = tools.path().join("fake-codex");
        let platform_argument = if std::env::consts::OS == "macos" {
            "--dangerously-bypass-approvals-and-sandbox"
        } else {
            "workspace-write"
        };
        std::fs::write(
            &script,
            format!(
                "#!/bin/sh\ntest \"${{1:-}}\" = \"--version\" && exit 0\ntrap 'code=$?; printf %s \"$code\" > \"$HOME/workspace/exit-code\"' EXIT\nfound_exec=false\nfound_json=false\nfound_ephemeral=false\nfound_skip=false\nfound_stdin=false\nfound_platform_arg=false\nfor arg in \"$@\"; do\n  test \"$arg\" != \"prompt-secret\" || exit 41\n  test \"$arg\" != 'exec' || found_exec=true\n  test \"$arg\" != '--json' || found_json=true\n  test \"$arg\" != '--ephemeral' || found_ephemeral=true\n  test \"$arg\" != '--skip-git-repo-check' || found_skip=true\n  test \"$arg\" != '-' || found_stdin=true\n  test \"$arg\" != '{platform_argument}' || found_platform_arg=true\ndone\n$found_exec && $found_json && $found_ephemeral && $found_skip && $found_stdin && $found_platform_arg || exit 46\nIFS= read -r prompt\ntest \"$prompt\" = \"prompt-secret\" || exit 42\ntest ! -r '{}/secrets.json' || exit 43\ntest ! -r '{}/private' || exit 44\ntest -z \"${{USER+x}}\" || exit 45\ntest -d \"$CODEX_HOME\" || exit 47\ncase \"$TMPDIR\" in \"$HOME/runs\") ;; *) exit 48 ;; esac\nprintf changed > \"$HOME/workspace/result\"\nprintf temporary > \"$TMPDIR/driver-tmp\"\nprintf '%s\\n' '{{\"type\":\"thread.started\",\"thread_id\":\"test\"}}' '{{\"type\":\"turn.completed\"}}'\n",
                state.display(),
                other.display()
            ),
        )
        .unwrap();
        std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o700)).unwrap();
        let environment = DriverEnvironment {
            state_dir: state.clone(),
            agents_root: state.join("agents"),
            agent_home: current.clone(),
            workspace: current.join("workspace"),
            codex_home: current.join("drivers/codex"),
            socket_path: socket,
            run_token: "run-token".to_owned(),
            path: std::env::var("PATH").unwrap(),
            codex_api_key: None,
        };
        let driver = CodexDriver::with_executable(script);
        let mut process = driver
            .start(DriverRun {
                prompt: "prompt-secret\n".to_owned(),
                environment,
            })
            .await
            .unwrap();
        let (events, mut receiver) = tokio::sync::mpsc::channel(8);
        let status = driver.observe(&mut process, &events).await.unwrap();
        drop(events);
        assert_eq!(
            status,
            DriverOutcome::Completed,
            "fake Driver exit code: {}",
            std::fs::read_to_string(current.join("workspace/exit-code"))
                .unwrap_or_else(|_| "missing".to_owned())
        );
        assert_eq!(
            std::fs::read_to_string(current.join("workspace/result")).unwrap(),
            "changed"
        );
        assert_eq!(
            receiver.recv().await.unwrap(),
            DriverEvent::OutputReceived {
                event_type: "thread.started".to_owned()
            }
        );
        assert_eq!(
            receiver.recv().await.unwrap(),
            DriverEvent::OutputReceived {
                event_type: "turn.completed".to_owned()
            }
        );
    }

    #[tokio::test]
    async fn git_workspace_detection_validates_repository_and_discovers_parent() {
        if find_on_path("git").is_none() {
            return;
        }
        let root = tempfile::tempdir().unwrap();
        let fake = root.path().join("fake");
        std::fs::create_dir(&fake).unwrap();
        std::fs::create_dir(fake.join(".git")).unwrap();
        assert!(!workspace_is_git_repository(&fake, &std::env::var("PATH").unwrap()).await);

        let repository = root.path().join("repository");
        std::fs::create_dir(&repository).unwrap();
        let initialized = std::process::Command::new("git")
            .args(["init", "--quiet"])
            .current_dir(&repository)
            .status()
            .unwrap();
        assert!(initialized.success());
        let nested = repository.join("nested/workspace");
        std::fs::create_dir_all(&nested).unwrap();
        assert!(workspace_is_git_repository(&nested, &std::env::var("PATH").unwrap()).await);
    }

    #[tokio::test]
    async fn turn_failed_is_failure_even_when_process_exits_zero() {
        if std::env::consts::OS == "linux" && find_on_path("bwrap").is_none() {
            return;
        }
        let root = tempfile::tempdir().unwrap();
        let tools = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        let home = state.join("agents/current");
        std::fs::create_dir_all(home.join("workspace")).unwrap();
        std::fs::create_dir_all(home.join("drivers/codex")).unwrap();
        let socket = state.join("daemon.sock");
        std::fs::write(&socket, "").unwrap();
        let script = tools.path().join("fake-codex");
        std::fs::write(
            &script,
            "#!/bin/sh\ntest \"${1:-}\" = \"--version\" && exit 0\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"turn.failed\"}'\nexit 0\n",
        )
        .unwrap();
        std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o700)).unwrap();
        let environment = DriverEnvironment {
            state_dir: state.clone(),
            agents_root: state.join("agents"),
            agent_home: home.clone(),
            workspace: home.join("workspace"),
            codex_home: home.join("drivers/codex"),
            socket_path: socket,
            run_token: "run-token".to_owned(),
            path: std::env::var("PATH").unwrap(),
            codex_api_key: None,
        };
        let driver = CodexDriver::with_executable(script);
        let mut process = driver
            .start(DriverRun {
                prompt: "run\n".to_owned(),
                environment,
            })
            .await
            .unwrap();
        let (events, _receiver) = tokio::sync::mpsc::channel(8);
        assert_eq!(
            driver.observe(&mut process, &events).await.unwrap(),
            DriverOutcome::Failed
        );
    }
}
