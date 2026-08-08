use std::{net::SocketAddr, path::PathBuf};

use anyhow::{Context, Result, ensure};
use figment::{
    Figment,
    providers::{Env, Format, Serialized, Toml},
};
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use url::Url;

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default)]
pub(crate) struct SumiConfig {
    pub(crate) server: ServerConfig,
    pub(crate) computer: ComputerConfig,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(default)]
pub(crate) struct ServerConfig {
    pub(crate) bind: SocketAddr,
    pub(crate) database_url: String,
    pub(crate) web_dist: PathBuf,
    pub(crate) attachment_dir: PathBuf,
    pub(crate) company_drive_dir: Option<PathBuf>,
    pub(crate) attachment_s3: Option<AttachmentS3Config>,
    pub(crate) attachment_max_bytes: u64,
    pub(crate) secure_cookies: bool,
    pub(crate) session_ttl_hours: i64,
    pub(crate) auth_ip_attempts_per_minute: u32,
    pub(crate) auth_email_attempts_per_minute: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct AttachmentS3Config {
    pub(crate) bucket: String,
    pub(crate) region: String,
    pub(crate) endpoint: Option<String>,
    pub(crate) allow_http: bool,
}

#[derive(Clone, Deserialize, Serialize)]
pub(crate) struct BuiltinOpenAiConfig {
    pub(crate) api_base: Url,
    pub(crate) token: ConfigSecret,
    pub(crate) model: String,
}

#[derive(Clone)]
pub(crate) struct ConfigSecret(SecretString);

impl ConfigSecret {
    pub(crate) fn expose(&self) -> &str {
        self.0.expose_secret()
    }

    pub(crate) fn clone_secret(&self) -> SecretString {
        self.0.clone()
    }
}

impl From<String> for ConfigSecret {
    fn from(value: String) -> Self {
        Self(SecretString::from(value))
    }
}

impl From<&str> for ConfigSecret {
    fn from(value: &str) -> Self {
        Self(SecretString::from(value.to_owned()))
    }
}

impl std::fmt::Debug for ConfigSecret {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("[REDACTED]")
    }
}

impl<'de> Deserialize<'de> for ConfigSecret {
    fn deserialize<D>(deserializer: D) -> std::result::Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        String::deserialize(deserializer).map(|value| Self(SecretString::from(value)))
    }
}

impl Serialize for ConfigSecret {
    fn serialize<S>(&self, serializer: S) -> std::result::Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(self.expose())
    }
}

impl std::fmt::Debug for BuiltinOpenAiConfig {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("BuiltinOpenAiConfig")
            .field("api_base", &self.api_base)
            .field("token", &"[REDACTED]")
            .field("model", &self.model)
            .finish()
    }
}

impl Default for ServerConfig {
    fn default() -> Self {
        Self {
            bind: "127.0.0.1:3000"
                .parse()
                .expect("valid default bind address"),
            database_url: "postgres://localhost/sumi_prod".to_owned(),
            web_dist: PathBuf::from("web/dist"),
            attachment_dir: default_sumi_dir().join("server/attachments"),
            company_drive_dir: None,
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
pub(crate) struct ComputerConfig {
    pub(crate) server_url: Url,
    pub(crate) state_dir: PathBuf,
    pub(crate) open_pairing_browser: bool,
    pub(crate) company_drive_root: Option<PathBuf>,
    pub(crate) codex_config_source: Option<PathBuf>,
    pub(crate) codex_auth_source: Option<PathBuf>,
    pub(crate) builtin: Option<BuiltinOpenAiConfig>,
    pub(crate) max_concurrent_runs: usize,
    pub(crate) per_agent_timeout_seconds: u64,
    pub(crate) shutdown_grace_period_seconds: u64,
}

impl Default for ComputerConfig {
    fn default() -> Self {
        Self {
            server_url: Url::parse("http://127.0.0.1:3000").expect("valid default server URL"),
            state_dir: default_computer_state_dir(),
            open_pairing_browser: true,
            company_drive_root: None,
            codex_config_source: None,
            codex_auth_source: None,
            builtin: None,
            max_concurrent_runs: 1000,
            per_agent_timeout_seconds: 30 * 60,
            shutdown_grace_period_seconds: 20,
        }
    }
}

pub(crate) fn load(path: Option<&PathBuf>) -> Result<SumiConfig> {
    let path = path.cloned().or_else(|| {
        let default = default_config_path();
        default.is_file().then_some(default)
    });
    let mut source = Figment::from(Serialized::defaults(SumiConfig::default()));
    if let Some(path) = &path {
        source = source.merge(Toml::file(path));
    }

    let config: SumiConfig = source
        .merge(Env::prefixed("SUMI_").split("__"))
        .extract()
        .context("invalid Sumi configuration")?;
    validate(&config)?;
    validate_config_file_permissions(path.as_ref(), &config)?;
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
    if let Some(builtin) = &config.computer.builtin {
        ensure!(
            matches!(builtin.api_base.scheme(), "http" | "https"),
            "computer.builtin.api_base must use http or https"
        );
        ensure!(
            builtin.api_base.username().is_empty() && builtin.api_base.password().is_none(),
            "computer.builtin.api_base must not contain credentials"
        );
        ensure!(
            builtin.api_base.query().is_none() && builtin.api_base.fragment().is_none(),
            "computer.builtin.api_base must not contain a query or fragment"
        );
        ensure!(
            !builtin.token.expose().trim().is_empty(),
            "computer.builtin.token must not be empty"
        );
        ensure!(
            !builtin.model.trim().is_empty(),
            "computer.builtin.model must not be empty"
        );
    }
    Ok(())
}

#[cfg(unix)]
fn validate_config_file_permissions(path: Option<&PathBuf>, config: &SumiConfig) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;

    let Some(path) = path.filter(|_| config.computer.builtin.is_some()) else {
        return Ok(());
    };
    let metadata = std::fs::symlink_metadata(path)
        .with_context(|| format!("failed to inspect Sumi configuration at {}", path.display()))?;
    ensure!(
        metadata.file_type().is_file(),
        "Sumi configuration must be a regular file when it contains computer.builtin.token"
    );
    ensure!(
        metadata.permissions().mode() & 0o077 == 0,
        "Sumi configuration containing computer.builtin.token must not be accessible by group or other users"
    );
    Ok(())
}

#[cfg(not(unix))]
fn validate_config_file_permissions(_path: Option<&PathBuf>, _config: &SumiConfig) -> Result<()> {
    Ok(())
}

pub(crate) fn default_computer_state_dir() -> PathBuf {
    default_sumi_dir().join("computer")
}

pub(crate) fn default_config_path() -> PathBuf {
    default_sumi_dir().join("config.toml")
}

pub(crate) fn default_sumi_dir() -> PathBuf {
    std::env::var_os("HOME")
        .map(|home| PathBuf::from(home).join(".sumi"))
        .unwrap_or_else(|| PathBuf::from(".sumi"))
}

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

pub(crate) fn daemon_socket_path(state_dir: &std::path::Path) -> PathBuf {
    runtime_dir_for(state_dir).join("daemon.sock")
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
    fn builtin_configuration_is_complete_private_and_redacted() {
        let directory = tempdir().unwrap();
        let path = directory.path().join("sumi.toml");
        fs::write(
            &path,
            "[computer.builtin]\napi_base = 'https://api.example.test/v1'\ntoken = 'provider-secret'\nmodel = 'test-model'\n",
        )
        .unwrap();

        let error = load(Some(&path)).unwrap_err();
        assert!(error.to_string().contains("group or other users"));

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
        }

        let config = load(Some(&path)).unwrap();
        let builtin = config.computer.builtin.as_ref().unwrap();
        assert_eq!(builtin.api_base.as_str(), "https://api.example.test/v1");
        assert_eq!(builtin.model, "test-model");
        assert!(!format!("{:?}", config.computer).contains("provider-secret"));
    }

    #[test]
    fn local_defaults_use_sumi_home_with_separate_boundaries() {
        let config = SumiConfig::default();
        assert_eq!(config.server.database_url, "postgres://localhost/sumi_prod");
        assert_eq!(config.computer.max_concurrent_runs, 1000);
        let home = std::env::var_os("HOME").map(PathBuf::from);
        if let Some(home) = home {
            assert_eq!(default_config_path(), home.join(".sumi/config.toml"));
            assert_eq!(config.computer.state_dir, home.join(".sumi/computer"));
            assert_eq!(
                config.server.attachment_dir,
                home.join(".sumi/server/attachments")
            );
            assert_eq!(config.server.company_drive_dir, None);
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
