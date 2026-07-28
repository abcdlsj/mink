use std::{net::SocketAddr, path::PathBuf};

use anyhow::{Context, Result, ensure};
use figment::{
    Figment,
    providers::{Env, Format, Serialized, Toml},
};
use serde::{Deserialize, Serialize};
use url::Url;

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct SumiConfig {
    pub server: ServerConfig,
    pub computer: ComputerConfig,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(default)]
pub struct ServerConfig {
    pub bind: SocketAddr,
    pub database_url: String,
    pub web_dist: PathBuf,
    pub attachment_dir: PathBuf,
    pub attachment_s3: Option<AttachmentS3Config>,
    pub attachment_max_bytes: u64,
    pub secure_cookies: bool,
    pub session_ttl_hours: i64,
    pub auth_ip_attempts_per_minute: u32,
    pub auth_email_attempts_per_minute: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct AttachmentS3Config {
    pub bucket: String,
    pub region: String,
    pub endpoint: Option<String>,
    pub allow_http: bool,
}

impl Default for ServerConfig {
    fn default() -> Self {
        Self {
            bind: "127.0.0.1:3000"
                .parse()
                .expect("valid default bind address"),
            database_url: "postgres://localhost/sumi_dev".to_owned(),
            web_dist: PathBuf::from("web/dist"),
            attachment_dir: default_sumi_dir().join("server/attachments"),
            attachment_s3: None,
            attachment_max_bytes: 100 * 1024 * 1024,
            secure_cookies: false,
            session_ttl_hours: 24 * 14,
            auth_ip_attempts_per_minute: 20,
            auth_email_attempts_per_minute: 6,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(default)]
pub struct ComputerConfig {
    pub server_url: Url,
    pub state_dir: PathBuf,
    pub open_pairing_browser: bool,
    pub codex_config_source: Option<PathBuf>,
    pub codex_auth_source: Option<PathBuf>,
    pub builtin_settings_source: Option<PathBuf>,
    pub builtin_models_source: Option<PathBuf>,
    pub builtin_auth_source: Option<PathBuf>,
    pub max_concurrent_runs: usize,
    pub per_agent_timeout_seconds: u64,
    pub shutdown_grace_period_seconds: u64,
}

impl Default for ComputerConfig {
    fn default() -> Self {
        Self {
            server_url: Url::parse("http://127.0.0.1:3000").expect("valid default server URL"),
            state_dir: default_computer_state_dir(),
            open_pairing_browser: true,
            codex_config_source: None,
            codex_auth_source: None,
            builtin_settings_source: None,
            builtin_models_source: None,
            builtin_auth_source: None,
            max_concurrent_runs: std::thread::available_parallelism()
                .map(|count| (count.get() / 2).max(1))
                .unwrap_or(1),
            per_agent_timeout_seconds: 30 * 60,
            shutdown_grace_period_seconds: 20,
        }
    }
}

pub fn load(path: Option<&PathBuf>) -> Result<SumiConfig> {
    let mut source = Figment::from(Serialized::defaults(SumiConfig::default()));
    if let Some(path) = path {
        source = source.merge(Toml::file(path));
    }

    let config: SumiConfig = source
        .merge(Env::prefixed("SUMI_").split("__"))
        .extract()
        .context("invalid Sumi configuration")?;
    validate(&config)?;
    Ok(config)
}

fn validate(config: &SumiConfig) -> Result<()> {
    ensure!(
        config.server.session_ttl_hours > 0,
        "server.session_ttl_hours must be positive"
    );
    ensure!(
        config.server.auth_ip_attempts_per_minute > 0,
        "server.auth_ip_attempts_per_minute must be positive"
    );
    ensure!(
        config.server.auth_email_attempts_per_minute > 0,
        "server.auth_email_attempts_per_minute must be positive"
    );
    ensure!(
        (1..=100 * 1024 * 1024).contains(&config.server.attachment_max_bytes),
        "server.attachment_max_bytes must be between 1 and 104857600"
    );
    if let Some(s3) = &config.server.attachment_s3 {
        ensure!(
            !s3.bucket.trim().is_empty(),
            "server.attachment_s3.bucket must not be empty"
        );
        ensure!(
            !s3.region.trim().is_empty(),
            "server.attachment_s3.region must not be empty"
        );
    }
    ensure!(
        config.computer.max_concurrent_runs > 0,
        "computer.max_concurrent_runs must be positive"
    );
    ensure!(
        config.computer.per_agent_timeout_seconds > 0,
        "computer.per_agent_timeout_seconds must be positive"
    );
    ensure!(
        config.computer.shutdown_grace_period_seconds > 0,
        "computer.shutdown_grace_period_seconds must be positive"
    );
    let builtin_source_count = [
        &config.computer.builtin_settings_source,
        &config.computer.builtin_models_source,
        &config.computer.builtin_auth_source,
    ]
    .into_iter()
    .filter(|path| path.is_some())
    .count();
    ensure!(
        matches!(builtin_source_count, 0 | 3),
        "computer Builtin settings, models, and auth source paths must be configured together"
    );
    Ok(())
}

pub(crate) fn default_computer_state_dir() -> PathBuf {
    default_sumi_dir().join("computer")
}

/// Sumi 的本机持久化根目录。所有 daemon、Agent 和本地 Server 文件都必须位于此边界内。
pub(crate) fn default_sumi_dir() -> PathBuf {
    std::env::var_os("HOME")
        .map(|home| PathBuf::from(home).join(".sumi"))
        .unwrap_or_else(|| PathBuf::from(".sumi"))
}

/// Runtime 与 Computer 状态分离，避免 socket 和临时文件污染持久状态目录。
pub(crate) fn runtime_dir_for(state_dir: &std::path::Path) -> PathBuf {
    if let (Some(computer_id), Some(space_dir)) = (state_dir.file_name(), state_dir.parent())
        && let Some(computer_root) = space_dir.parent()
        && computer_root
            .file_name()
            .is_some_and(|name| name == "computer")
        && let Some(sumi_root) = computer_root.parent()
    {
        return sumi_root.join("runtime").join(computer_id);
    }
    state_dir
        .parent()
        .map(|parent| parent.join("runtime"))
        .unwrap_or_else(|| state_dir.join("runtime"))
}

#[cfg(test)]
mod tests {
    use std::fs;

    use tempfile::tempdir;

    use super::*;

    #[test]
    fn file_values_override_defaults() {
        let directory = tempdir().unwrap();
        let path = directory.path().join("sumi.toml");
        fs::write(
            &path,
            "[server]\nbind = '127.0.0.1:4100'\ndatabase_url = 'postgres://localhost/test'\n",
        )
        .unwrap();

        let config = load(Some(&path)).unwrap();

        assert_eq!(config.server.bind.port(), 4100);
        assert_eq!(config.server.database_url, "postgres://localhost/test");
        assert_eq!(config.server.web_dist, PathBuf::from("web/dist"));
    }

    #[test]
    fn zero_auth_rate_limit_is_rejected_during_configuration_loading() {
        let directory = tempdir().unwrap();
        let path = directory.path().join("sumi.toml");
        fs::write(&path, "[server]\nauth_ip_attempts_per_minute = 0\n").unwrap();

        let error = load(Some(&path)).unwrap_err();

        assert!(error.to_string().contains("must be positive"));
    }

    #[test]
    fn partial_builtin_sources_are_rejected_during_configuration_loading() {
        let directory = tempdir().unwrap();
        let path = directory.path().join("sumi.toml");
        fs::write(
            &path,
            "[computer]\nbuiltin_settings_source = 'settings.json'\n",
        )
        .unwrap();

        let error = load(Some(&path)).unwrap_err();

        assert!(error.to_string().contains("must be configured together"));
    }

    #[test]
    fn local_defaults_use_sumi_home_with_separate_boundaries() {
        let config = SumiConfig::default();
        let home = std::env::var_os("HOME").map(PathBuf::from);
        if let Some(home) = home {
            assert_eq!(config.computer.state_dir, home.join(".sumi/computer"));
            assert_eq!(
                config.server.attachment_dir,
                home.join(".sumi/server/attachments")
            );
            assert_eq!(
                runtime_dir_for(&config.computer.state_dir),
                home.join(".sumi/runtime")
            );
        }
    }

    #[test]
    fn id_scoped_computer_state_uses_short_id_scoped_runtime_path() {
        let state = PathBuf::from(
            "/tmp/.sumi/computer/019fa900-0000-7000-8000-000000000001/\
             019fa900-0000-7000-8000-000000000002",
        );

        assert_eq!(
            runtime_dir_for(&state),
            PathBuf::from("/tmp/.sumi/runtime/019fa900-0000-7000-8000-000000000002")
        );
    }
}
