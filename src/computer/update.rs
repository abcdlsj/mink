use std::{
    path::{Component, Path, PathBuf},
    process::Stdio,
    time::Duration,
};

use anyhow::{Context, ensure};
use semver::Version;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio::{io::AsyncWriteExt, process::Command, sync::mpsc};

use crate::{
    cli::{ComputerArgs, UpdaterArgs},
    config::{ComputerConfig, default_sumi_dir},
    protocol::update::{ComputerRelease, current_target},
};

const MAX_ARTIFACT_BYTES: usize = 200 * 1024 * 1024;

#[derive(Clone, Debug)]
pub(crate) struct StagedUpdate {
    pub(crate) version: String,
    pub(crate) candidate: PathBuf,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct UpdateJournal {
    failed_version: Option<String>,
    last_result: Option<String>,
    last_version: Option<String>,
}

pub(crate) async fn install_release_binary_if_needed(
    args: &ComputerArgs,
    auto_update: bool,
) -> anyhow::Result<()> {
    if cfg!(debug_assertions) || !auto_update {
        return Ok(());
    }
    let current = std::env::current_exe()?;
    let installed = default_sumi_dir().join("bin/sumi");
    if same_file(&current, &installed) {
        return Ok(());
    }
    if !installed.is_file()
        && let Err(error) = install_binary(&current, &installed).await
    {
        tracing::warn!(%error, path = %installed.display(), "Computer could not install its stable executable; continuing from the current path");
        return Ok(());
    }
    tracing::info!(path = %installed.display(), "Computer installed its stable executable");
    exec_computer(&installed, args);
    Ok(())
}

fn same_file(left: &Path, right: &Path) -> bool {
    let Ok(left) = std::fs::canonicalize(left) else {
        return false;
    };
    std::fs::canonicalize(right).is_ok_and(|right| right == left)
}

async fn install_binary(source: &Path, installed: &Path) -> anyhow::Result<()> {
    let parent = installed
        .parent()
        .context("stable Computer path has no parent")?;
    ensure_private_directory(parent).await?;
    let pending = parent.join(format!("sumi.installing-{}", std::process::id()));
    tokio::fs::copy(source, &pending).await?;
    make_executable(&pending).await?;
    let file = tokio::fs::OpenOptions::new()
        .write(true)
        .open(&pending)
        .await?;
    file.sync_all().await?;
    drop(file);
    match tokio::fs::hard_link(&pending, installed).await {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
        Err(error) => return Err(error.into()),
    }
    tokio::fs::remove_file(pending).await?;
    Ok(())
}

#[cfg(unix)]
fn exec_computer(executable: &Path, args: &ComputerArgs) {
    use std::os::unix::process::CommandExt;

    let mut command = std::process::Command::new(executable);
    command.arg("computer");
    if let Some(config) = &args.config {
        command.arg("--config").arg(config);
    }
    if let Some(server) = &args.server {
        command.arg("--server").arg(server.as_str());
    }
    let error = command.exec();
    tracing::warn!(%error, path = %executable.display(), "Computer could not restart from its stable executable; continuing from the current path");
}

#[cfg(not(unix))]
fn exec_computer(_executable: &Path, _args: &ComputerArgs) {}

pub(crate) fn start_checker(
    config: &ComputerConfig,
    server: url::Url,
    computer_home: PathBuf,
) -> Option<mpsc::Receiver<StagedUpdate>> {
    if !config.auto_update {
        tracing::info!("Computer automatic updates are disabled by local policy");
        return None;
    }
    let interval = Duration::from_secs(config.update_check_interval_seconds);
    let (sender, receiver) = mpsc::channel(1);
    tokio::spawn(async move {
        loop {
            match check_and_stage(&server, &computer_home).await {
                Ok(Some(update)) => {
                    tracing::info!(target_version = %update.version, "Computer update staged");
                    loop {
                        if local_runs_are_inactive(&computer_home.join("daemon.db")).await
                            && sender.send(update.clone()).await.is_err()
                        {
                            return;
                        }
                        tokio::time::sleep(Duration::from_secs(5)).await;
                    }
                }
                Ok(None) => {}
                Err(error) => tracing::warn!(%error, "Computer update check failed"),
            }
            tokio::time::sleep(interval).await;
        }
    });
    Some(receiver)
}

async fn check_and_stage(
    server: &url::Url,
    computer_home: &Path,
) -> anyhow::Result<Option<StagedUpdate>> {
    ensure_secure_update_origin(server)?;
    let client = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let endpoint = server.join("api/v1/computer-updates/stable/manifest")?;
    let response = client.get(endpoint).send().await?;
    if response.status() == reqwest::StatusCode::NOT_FOUND {
        return Ok(None);
    }
    let release = response
        .error_for_status()?
        .json::<ComputerRelease>()
        .await
        .context("invalid Computer release manifest")?;
    ensure!(
        release.target == current_target(),
        "release target does not match this Computer"
    );
    ensure!(
        valid_artifact_name(&release.artifact),
        "release artifact name is invalid"
    );

    let current = Version::parse(env!("CARGO_PKG_VERSION"))?;
    let candidate = Version::parse(&release.version)?;
    if candidate <= current {
        return Ok(None);
    }
    let update_root = computer_home.join("update");
    let journal = read_journal(&update_root.join("journal.json")).await?;
    if journal.failed_version.as_deref() == Some(release.version.as_str())
        || (journal.last_result.as_deref() == Some("succeeded")
            && journal.last_version.as_deref() == Some(release.version.as_str()))
    {
        return Ok(None);
    }

    let staged_dir = update_root.join("staged").join(&release.version);
    ensure_private_directory(&staged_dir).await?;
    let staged = staged_dir.join("sumi");
    if file_sha256(&staged).await.as_deref() != Some(release.sha256.as_str()) {
        let artifact_url = server.join(&format!(
            "api/v1/computer-updates/stable/files/{}",
            release.artifact
        ))?;
        let response = client.get(artifact_url).send().await?.error_for_status()?;
        if let Some(length) = response.content_length() {
            ensure!(
                length <= MAX_ARTIFACT_BYTES as u64,
                "Computer release artifact is too large"
            );
        }
        let content = response.bytes().await?;
        ensure!(
            content.len() <= MAX_ARTIFACT_BYTES,
            "Computer release artifact is too large"
        );
        let actual = hex::encode(Sha256::digest(&content));
        ensure!(
            actual == release.sha256,
            "Computer release artifact hash mismatch"
        );
        write_executable_atomically(&staged, &content).await?;
    }
    Ok(Some(StagedUpdate {
        version: release.version,
        candidate: staged,
    }))
}

fn ensure_secure_update_origin(server: &url::Url) -> anyhow::Result<()> {
    let local_http = server.scheme() == "http"
        && match server.host() {
            Some(url::Host::Ipv4(address)) => address.is_loopback(),
            Some(url::Host::Ipv6(address)) => address.is_loopback(),
            Some(url::Host::Domain(domain)) => {
                domain == "localhost" || domain.ends_with(".localhost")
            }
            None => false,
        };
    ensure!(
        server.scheme() == "https" || local_http,
        "Computer automatic updates require HTTPS except on localhost"
    );
    Ok(())
}

fn valid_artifact_name(value: &str) -> bool {
    let mut components = Path::new(value).components();
    matches!(components.next(), Some(Component::Normal(_))) && components.next().is_none()
}

async fn local_runs_are_inactive(database: &Path) -> bool {
    let options = sqlx::sqlite::SqliteConnectOptions::new()
        .filename(database)
        .read_only(true)
        .busy_timeout(Duration::from_secs(1));
    let Ok(mut connection) = sqlx::SqliteConnection::connect_with(&options).await else {
        return false;
    };
    use sqlx::Connection;
    sqlx::query_scalar::<_, i64>(
        "SELECT count(*) FROM local_runs WHERE state IN ('queued','starting','running','finalizing','stopping')",
    )
    .fetch_one(&mut connection)
    .await
    .is_ok_and(|count| count == 0)
}

pub(crate) async fn launch_updater(
    update: &StagedUpdate,
    computer_home: &Path,
    args: &ComputerArgs,
    ready_timeout_seconds: u64,
) -> anyhow::Result<()> {
    let current_exe = std::env::current_exe()?;
    let helper = computer_home.join("update").join("updater");
    tokio::fs::copy(&current_exe, &helper).await?;
    make_executable(&helper).await?;
    let mut command = Command::new(&helper);
    command
        .arg("updater")
        .arg("--parent-pid")
        .arg(std::process::id().to_string())
        .arg("--current-exe")
        .arg(&current_exe)
        .arg("--candidate")
        .arg(&update.candidate)
        .arg("--version")
        .arg(&update.version)
        .arg("--computer-home")
        .arg(computer_home)
        .arg("--ready-timeout-seconds")
        .arg(ready_timeout_seconds.to_string())
        .stdin(Stdio::null());
    if let Some(config) = &args.config {
        command.arg("--config").arg(config);
    }
    if let Some(server) = &args.server {
        command.arg("--server").arg(server.as_str());
    }
    command
        .spawn()
        .context("failed to start Computer updater")?;
    tracing::info!(target_version = %update.version, "Computer updater started");
    Ok(())
}

pub(crate) async fn run_updater(args: UpdaterArgs) -> anyhow::Result<()> {
    wait_for_parent(args.parent_pid).await?;
    let update_root = args.computer_home.join("update");
    if let Err(error) = ensure_private_directory(&update_root).await {
        restart_after_prepare_failure(&args, error, &update_root).await?;
        return Ok(());
    }
    let previous = update_root.join("previous-sumi");
    let database = args.computer_home.join("daemon.db");
    let backup_dir = update_root.join("database-backup");
    let replacement = args.current_exe.with_extension("update-new");
    let prepared = async {
        tokio::fs::copy(&args.current_exe, &previous).await?;
        make_executable(&previous).await?;
        backup_database(&database, &backup_dir).await?;
        tokio::fs::copy(&args.candidate, &replacement).await?;
        make_executable(&replacement).await?;
        tokio::fs::rename(&replacement, &args.current_exe).await?;
        Ok::<_, anyhow::Error>(())
    }
    .await;
    if let Err(error) = prepared {
        restart_after_prepare_failure(&args, error, &update_root).await?;
        return Ok(());
    }

    let ready = update_root.join(format!("ready-{}", args.version));
    let _ = tokio::fs::remove_file(&ready).await;
    let mut child =
        match spawn_computer(&args.current_exe, &args.config, &args.server, Some(&ready)) {
            Ok(child) => child,
            Err(error) => {
                rollback_and_restart(&args, &previous, &database, &backup_dir, &update_root)
                    .await?;
                return Err(error);
            }
        };
    let healthy = wait_for_ready(&mut child, &ready, args.ready_timeout_seconds).await;
    if healthy {
        write_journal(
            &update_root.join("journal.json"),
            &UpdateJournal {
                failed_version: None,
                last_result: Some("succeeded".into()),
                last_version: Some(args.version.clone()),
            },
        )
        .await?;
        tracing::info!(target_version = %args.version, "Computer update succeeded");
        return Ok(());
    }

    let _ = child.kill().await;
    let _ = child.wait().await;
    rollback_and_restart(&args, &previous, &database, &backup_dir, &update_root).await?;
    Ok(())
}

async fn restart_after_prepare_failure(
    args: &UpdaterArgs,
    error: anyhow::Error,
    update_root: &Path,
) -> anyhow::Result<()> {
    tracing::error!(%error, target_version = %args.version, "Computer update preparation failed; restarting the existing daemon");
    let journal = write_journal(
        &update_root.join("journal.json"),
        &UpdateJournal {
            failed_version: Some(args.version.clone()),
            last_result: Some("failed".into()),
            last_version: Some(args.version.clone()),
        },
    )
    .await;
    let restarted = spawn_computer(&args.current_exe, &args.config, &args.server, None);
    restarted?;
    journal
}

async fn rollback_and_restart(
    args: &UpdaterArgs,
    previous: &Path,
    database: &Path,
    backup_dir: &Path,
    update_root: &Path,
) -> anyhow::Result<()> {
    let rollback = args.current_exe.with_extension("rollback-new");
    tokio::fs::copy(previous, &rollback).await?;
    make_executable(&rollback).await?;
    tokio::fs::rename(&rollback, &args.current_exe).await?;
    restore_database(database, backup_dir).await?;
    write_journal(
        &update_root.join("journal.json"),
        &UpdateJournal {
            failed_version: Some(args.version.clone()),
            last_result: Some("rolled_back".into()),
            last_version: Some(args.version.clone()),
        },
    )
    .await?;
    spawn_computer(&args.current_exe, &args.config, &args.server, None)?;
    tracing::warn!(target_version = %args.version, "Computer update rolled back");
    Ok(())
}

pub(crate) async fn signal_ready_from_environment() -> anyhow::Result<()> {
    let Some(path) = std::env::var_os("SUMI_UPDATE_READY_FILE") else {
        return Ok(());
    };
    let path = PathBuf::from(path);
    if let Some(parent) = path.parent() {
        ensure_private_directory(parent).await?;
    }
    let mut file = tokio::fs::File::create(&path).await?;
    file.write_all(b"ready\n").await?;
    file.sync_all().await?;
    Ok(())
}

async fn wait_for_parent(parent_pid: u32) -> anyhow::Result<()> {
    for _ in 0..150 {
        #[cfg(unix)]
        if unsafe { libc::kill(parent_pid as i32, 0) } == -1 {
            return Ok(());
        }
        #[cfg(not(unix))]
        return Ok(());
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    anyhow::bail!("Computer daemon did not exit for update")
}

fn spawn_computer(
    executable: &Path,
    config: &Option<PathBuf>,
    server: &Option<url::Url>,
    ready: Option<&Path>,
) -> anyhow::Result<tokio::process::Child> {
    let mut command = Command::new(executable);
    command.arg("computer").stdin(Stdio::null());
    if let Some(config) = config {
        command.arg("--config").arg(config);
    }
    if let Some(server) = server {
        command.arg("--server").arg(server.as_str());
    }
    if let Some(ready) = ready {
        command.env("SUMI_UPDATE_READY_FILE", ready);
    } else {
        command.env_remove("SUMI_UPDATE_READY_FILE");
    }
    command.spawn().context("failed to restart Computer daemon")
}

async fn wait_for_ready(child: &mut tokio::process::Child, ready: &Path, seconds: u64) -> bool {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(seconds);
    while tokio::time::Instant::now() < deadline {
        if child.try_wait().ok().flatten().is_some() {
            return false;
        }
        if ready.is_file() {
            tokio::time::sleep(Duration::from_secs(2)).await;
            return child.try_wait().ok().flatten().is_none();
        }
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    false
}

async fn backup_database(database: &Path, backup_dir: &Path) -> anyhow::Result<()> {
    if backup_dir.exists() {
        tokio::fs::remove_dir_all(backup_dir).await?;
    }
    ensure_private_directory(backup_dir).await?;
    for source in database_files(database) {
        if source.is_file() {
            let name = source
                .file_name()
                .context("database backup path has no name")?;
            tokio::fs::copy(&source, backup_dir.join(name)).await?;
        }
    }
    Ok(())
}

async fn restore_database(database: &Path, backup_dir: &Path) -> anyhow::Result<()> {
    for target in database_files(database) {
        if target.exists() {
            tokio::fs::remove_file(target).await?;
        }
    }
    let mut entries = tokio::fs::read_dir(backup_dir).await?;
    while let Some(entry) = entries.next_entry().await? {
        tokio::fs::copy(
            entry.path(),
            database
                .parent()
                .context("database has no parent")?
                .join(entry.file_name()),
        )
        .await?;
    }
    Ok(())
}

fn database_files(database: &Path) -> [PathBuf; 3] {
    [
        database.to_path_buf(),
        PathBuf::from(format!("{}-wal", database.display())),
        PathBuf::from(format!("{}-shm", database.display())),
    ]
}

async fn read_journal(path: &Path) -> anyhow::Result<UpdateJournal> {
    match tokio::fs::read(path).await {
        Ok(value) => Ok(serde_json::from_slice(&value)?),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(UpdateJournal::default()),
        Err(error) => Err(error.into()),
    }
}

async fn write_journal(path: &Path, journal: &UpdateJournal) -> anyhow::Result<()> {
    let encoded = serde_json::to_vec_pretty(journal)?;
    let pending = path.with_extension("json.new");
    let mut file = tokio::fs::File::create(&pending).await?;
    file.write_all(&encoded).await?;
    file.sync_all().await?;
    tokio::fs::rename(pending, path).await?;
    Ok(())
}

async fn file_sha256(path: &Path) -> Option<String> {
    tokio::fs::read(path)
        .await
        .ok()
        .map(|content| hex::encode(Sha256::digest(content)))
}

async fn write_executable_atomically(path: &Path, content: &[u8]) -> anyhow::Result<()> {
    let pending = path.with_extension("new");
    let mut file = tokio::fs::File::create(&pending).await?;
    file.write_all(content).await?;
    file.sync_all().await?;
    drop(file);
    make_executable(&pending).await?;
    tokio::fs::rename(pending, path).await?;
    Ok(())
}

async fn ensure_private_directory(path: &Path) -> anyhow::Result<()> {
    tokio::fs::create_dir_all(path).await?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700)).await?;
    }
    Ok(())
}

async fn make_executable(path: &Path) -> anyhow::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700)).await?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{Json, Router, routing::get};
    use tempfile::tempdir;

    #[test]
    fn artifact_name_cannot_escape_the_release_directory() {
        assert!(valid_artifact_name("sumi-aarch64-apple-darwin"));
        assert!(!valid_artifact_name("../sumi"));
        assert!(!valid_artifact_name("nested/sumi"));
        assert!(!valid_artifact_name("/tmp/sumi"));
    }

    #[test]
    fn update_origin_requires_https_except_on_loopback() {
        for value in [
            "https://sumi.example.test/",
            "http://localhost:3000/",
            "http://computer.localhost:3000/",
            "http://127.0.0.1:3000/",
            "http://[::1]:3000/",
        ] {
            ensure_secure_update_origin(&url::Url::parse(value).unwrap()).unwrap();
        }
        assert!(
            ensure_secure_update_origin(
                &url::Url::parse("http://sumi.example.test:3000/").unwrap()
            )
            .is_err()
        );
    }

    #[tokio::test]
    async fn first_install_creates_a_stable_executable_without_overwriting_it() {
        let directory = tempdir().unwrap();
        let source = directory.path().join("downloaded-sumi");
        let installed = directory.path().join("bin/sumi");
        tokio::fs::write(&source, b"first release").await.unwrap();

        install_binary(&source, &installed).await.unwrap();
        tokio::fs::write(&source, b"old downloaded copy")
            .await
            .unwrap();
        install_binary(&source, &installed).await.unwrap();

        assert_eq!(tokio::fs::read(&installed).await.unwrap(), b"first release");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                tokio::fs::metadata(installed)
                    .await
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o700
            );
        }
    }

    #[tokio::test]
    async fn same_origin_release_is_downloaded_verified_and_staged() {
        let artifact = b"computer binary".to_vec();
        let release = crate::protocol::update::ComputerRelease {
            version: "9.9.9".into(),
            protocol_version: 4,
            target: current_target().into(),
            artifact: "sumi-test".into(),
            sha256: hex::encode(Sha256::digest(&artifact)),
        };
        let app = Router::new()
            .route(
                "/api/v1/computer-updates/stable/manifest",
                get({
                    let release = release.clone();
                    move || async move { Json(release) }
                }),
            )
            .route(
                "/api/v1/computer-updates/stable/files/sumi-test",
                get({
                    let artifact = artifact.clone();
                    move || async move { artifact }
                }),
            );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        tokio::spawn(axum::serve(listener, app).into_future());
        let directory = tempdir().unwrap();

        let staged = check_and_stage(
            &url::Url::parse(&format!("http://{address}/")).unwrap(),
            directory.path(),
        )
        .await
        .unwrap()
        .unwrap();

        assert_eq!(staged.version, "9.9.9");
        assert_eq!(tokio::fs::read(staged.candidate).await.unwrap(), artifact);
    }

    #[tokio::test]
    async fn database_backup_restores_database_and_wal_files() {
        let directory = tempdir().unwrap();
        let database = directory.path().join("daemon.db");
        let wal = PathBuf::from(format!("{}-wal", database.display()));
        tokio::fs::write(&database, b"database-before")
            .await
            .unwrap();
        tokio::fs::write(&wal, b"wal-before").await.unwrap();
        let backup = directory.path().join("backup");
        backup_database(&database, &backup).await.unwrap();
        tokio::fs::write(&database, b"database-after")
            .await
            .unwrap();
        tokio::fs::remove_file(&wal).await.unwrap();

        restore_database(&database, &backup).await.unwrap();

        assert_eq!(tokio::fs::read(database).await.unwrap(), b"database-before");
        assert_eq!(tokio::fs::read(wal).await.unwrap(), b"wal-before");
    }
}
