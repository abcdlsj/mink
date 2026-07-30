use std::{collections::BTreeMap, fs, os::unix::fs::PermissionsExt, path::Path};

use anyhow::{Context, Result, ensure};
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Deserializer};
use subtle::ConstantTimeEq;
use url::Url;
use zeroize::Zeroizing;

use crate::config::ComputerConfig;

use super::provider::ProviderConfig;

const MAX_CONFIG_BYTES: u64 = 1024 * 1024;

#[derive(Clone)]
pub(super) struct LocalSecret(SecretString);

impl PartialEq for LocalSecret {
    fn eq(&self, other: &Self) -> bool {
        self.expose()
            .as_bytes()
            .ct_eq(other.expose().as_bytes())
            .into()
    }
}

impl Eq for LocalSecret {}

impl LocalSecret {
    fn new(value: String) -> Self {
        Self(SecretString::from(value))
    }

    fn expose(&self) -> &str {
        self.0.expose_secret()
    }
}

impl std::fmt::Debug for LocalSecret {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("[REDACTED]")
    }
}

impl<'de> Deserialize<'de> for LocalSecret {
    fn deserialize<D>(deserializer: D) -> std::result::Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        String::deserialize(deserializer).map(Self::new)
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(super) struct BuiltinAuthentication {
    pub(super) provider: String,
    pub(super) api_key: LocalSecret,
}

#[derive(Clone)]
pub(super) struct BuiltinProviderConfig {
    model: String,
    base_url: Url,
    authentication: BuiltinAuthentication,
}

impl BuiltinProviderConfig {
    pub(super) fn into_provider_config(self) -> ProviderConfig {
        ProviderConfig::openai(
            SecretString::from(self.authentication.api_key.expose().to_owned()),
            self.model,
        )
        .with_base_url(self.base_url.to_string().trim_end_matches('/').to_owned())
    }
}

#[derive(Deserialize)]
struct PiSettings {
    #[serde(rename = "defaultProvider")]
    default_provider: String,
    #[serde(rename = "defaultModel")]
    default_model: String,
}

#[derive(Deserialize)]
struct PiProvider {
    models: Vec<PiModel>,
}

#[derive(Deserialize)]
struct PiModel {
    id: String,
    api: String,
    #[serde(rename = "baseUrl")]
    base_url: String,
}

#[derive(Deserialize)]
struct PiAuthentication {
    #[serde(rename = "type")]
    kind: String,
    key: LocalSecret,
}

pub(super) fn load(config: &ComputerConfig) -> Result<Option<BuiltinProviderConfig>> {
    let paths = match (
        config.builtin_settings_source.as_deref(),
        config.builtin_models_source.as_deref(),
        config.builtin_auth_source.as_deref(),
    ) {
        (None, None, None) => return Ok(None),
        (Some(settings), Some(models), Some(auth)) => (settings, models, auth),
        _ => anyhow::bail!(
            "computer Builtin settings, models, and auth source paths must be configured together"
        ),
    };

    let settings: PiSettings = read_json(paths.0, false, "Builtin settings source")?;
    ensure!(
        !settings.default_provider.trim().is_empty(),
        "Builtin defaultProvider must not be empty"
    );
    ensure!(
        !settings.default_model.trim().is_empty(),
        "Builtin defaultModel must not be empty"
    );

    let providers: BTreeMap<String, PiProvider> =
        read_json(paths.1, false, "Builtin models source")?;
    let provider = providers.get(&settings.default_provider).with_context(|| {
        format!(
            "Builtin provider '{}' is not declared in the models source",
            settings.default_provider
        )
    })?;
    let model = provider
        .models
        .iter()
        .find(|model| model.id == settings.default_model)
        .with_context(|| {
            format!(
                "Builtin model '{}/{}' is not declared in the models source",
                settings.default_provider, settings.default_model
            )
        })?;
    ensure!(
        model.api == "openai-completions",
        "Builtin model '{}/{}' uses unsupported API kind '{}'",
        settings.default_provider,
        settings.default_model,
        model.api
    );
    let base_url = validate_base_url(&model.base_url)?;

    let mut authentication: BTreeMap<String, PiAuthentication> =
        read_json(paths.2, true, "Builtin auth source")?;
    let authentication = authentication
        .remove(&settings.default_provider)
        .with_context(|| {
            format!(
                "Builtin provider '{}' has no authentication entry",
                settings.default_provider
            )
        })?;
    ensure!(
        authentication.kind == "api_key",
        "Builtin provider '{}' uses unsupported authentication type '{}'",
        settings.default_provider,
        authentication.kind
    );
    ensure!(
        !authentication.key.expose().is_empty(),
        "Builtin provider '{}' API key must not be empty",
        settings.default_provider
    );

    Ok(Some(BuiltinProviderConfig {
        model: settings.default_model,
        base_url,
        authentication: BuiltinAuthentication {
            provider: settings.default_provider,
            api_key: authentication.key,
        },
    }))
}

fn read_json<T>(path: &Path, secret: bool, label: &str) -> Result<T>
where
    T: for<'de> Deserialize<'de>,
{
    let metadata = fs::symlink_metadata(path)
        .with_context(|| format!("failed to inspect {label} at {}", path.display()))?;
    ensure!(
        metadata.file_type().is_file(),
        "{label} must be a regular file: {}",
        path.display()
    );
    ensure!(
        metadata.len() <= MAX_CONFIG_BYTES,
        "{label} exceeds {MAX_CONFIG_BYTES} bytes"
    );
    if secret {
        ensure!(
            metadata.permissions().mode() & 0o077 == 0,
            "{label} must not be accessible by group or other users: {}",
            path.display()
        );
    }
    let bytes = Zeroizing::new(fs::read(path).with_context(|| format!("failed to read {label}"))?);
    serde_json::from_slice(&bytes).with_context(|| format!("{label} is not valid JSON"))
}

fn validate_base_url(value: &str) -> Result<Url> {
    let url = Url::parse(value).context("Builtin model baseUrl is invalid")?;
    ensure!(
        matches!(url.scheme(), "http" | "https"),
        "Builtin model baseUrl must use http or https"
    );
    ensure!(
        url.username().is_empty() && url.password().is_none(),
        "Builtin model baseUrl must not contain credentials"
    );
    ensure!(
        url.query().is_none() && url.fragment().is_none(),
        "Builtin model baseUrl must not contain a query or fragment"
    );
    Ok(url)
}

#[cfg(test)]
mod tests {
    use std::{fs, os::unix::fs::PermissionsExt, path::PathBuf};

    use tempfile::TempDir;

    use super::super::{
        provider::{OpenAiProvider, Provider},
        types::{Chunk, Message},
    };
    use super::*;

    fn sources(api: &str) -> (TempDir, ComputerConfig) {
        let directory = tempfile::tempdir().unwrap();
        let settings = directory.path().join("settings.json");
        let models = directory.path().join("models-store.json");
        let auth = directory.path().join("auth.json");
        fs::write(
            &settings,
            r#"{"defaultProvider":"local","defaultModel":"test-model"}"#,
        )
        .unwrap();
        fs::write(
            &models,
            format!(
                r#"{{"local":{{"models":[{{"id":"test-model","api":"{api}","baseUrl":"http://127.0.0.1:9"}}]}}}}"#
            ),
        )
        .unwrap();
        fs::write(
            &auth,
            r#"{"local":{"type":"api_key","key":"not-for-logs"}}"#,
        )
        .unwrap();
        fs::set_permissions(&auth, fs::Permissions::from_mode(0o600)).unwrap();
        let config = ComputerConfig {
            builtin_settings_source: Some(settings),
            builtin_models_source: Some(models),
            builtin_auth_source: Some(auth),
            ..ComputerConfig::default()
        };
        (directory, config)
    }

    #[test]
    fn loads_selected_pi_compatible_provider_without_exposing_authentication() {
        let (_directory, config) = sources("openai-completions");

        let loaded = load(&config).unwrap().unwrap();

        assert_eq!(loaded.authentication.provider, "local");
        assert_eq!(loaded.model, "test-model");
        assert_eq!(loaded.base_url.as_str(), "http://127.0.0.1:9/");
        assert_eq!(format!("{:?}", loaded.authentication.api_key), "[REDACTED]");
        assert!(!format!("{:?}", loaded.authentication).contains("not-for-logs"));
    }

    #[test]
    fn rejects_unsupported_api_and_permissive_auth_file() {
        let (_directory, config) = sources("google-generative-ai");
        let error = load(&config).err().unwrap();
        assert!(error.to_string().contains("unsupported API kind"));

        let (_directory, config) = sources("openai-completions");
        fs::set_permissions(
            config.builtin_auth_source.as_ref().unwrap(),
            fs::Permissions::from_mode(0o644),
        )
        .unwrap();
        let error = load(&config).err().unwrap();
        assert!(error.to_string().contains("group or other users"));
    }

    #[tokio::test]
    #[ignore = "manually contacts the selected local Pi provider"]
    async fn live_builtin_provider_smoke_from_pi_sources() {
        let pi_dir =
            PathBuf::from(std::env::var_os("HOME").expect("HOME is required")).join(".pi/agent");
        let config = ComputerConfig {
            builtin_settings_source: Some(pi_dir.join("settings.json")),
            builtin_models_source: Some(pi_dir.join("models-store.json")),
            builtin_auth_source: Some(pi_dir.join("auth.json")),
            ..ComputerConfig::default()
        };
        let loaded = load(&config)
            .expect("local Pi Builtin configuration must load")
            .expect("local Pi Builtin configuration must be present");
        assert_eq!(loaded.authentication.provider, "deepseek");
        assert_eq!(loaded.model, "deepseek-v4-pro");
        assert_eq!(loaded.base_url.as_str(), "https://api.deepseek.com/");

        let provider = OpenAiProvider::new(loaded.into_provider_config())
            .expect("Builtin provider must initialize");
        let mut stream = provider
            .chat_stream(&[Message::user("Reply with exactly OK.")], &[])
            .await
            .expect("Builtin provider request must succeed");
        let mut received_text = false;
        let mut received_done = false;
        while let Some(chunk) = stream.recv().await {
            match chunk {
                Chunk::Text { delta } => received_text |= !delta.is_empty(),
                Chunk::Done { .. } => received_done = true,
                Chunk::Error { .. } => panic!("Builtin provider stream returned an error"),
                Chunk::Reasoning { .. } | Chunk::ToolCall { .. } => {}
            }
        }
        assert!(received_text, "Builtin provider returned no text");
        assert!(received_done, "Builtin provider stream did not finish");
    }
}
