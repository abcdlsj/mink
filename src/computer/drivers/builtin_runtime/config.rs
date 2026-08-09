use anyhow::{Result, ensure};
use secrecy::SecretString;

use crate::config::ComputerConfig;

use super::provider::ProviderConfig;

#[derive(Clone)]
pub(super) struct BuiltinProviderConfig {
    model: String,
    api_base: String,
    token: SecretString,
    compaction_trigger_tokens: usize,
    compaction_keep_recent_tokens: usize,
}

impl BuiltinProviderConfig {
    pub(super) fn into_provider_config(self) -> ProviderConfig {
        ProviderConfig::openai(self.token, self.model).with_base_url(self.api_base)
    }

    pub(super) fn compaction_trigger_tokens(&self) -> usize {
        self.compaction_trigger_tokens
    }

    pub(super) fn compaction_keep_recent_tokens(&self) -> usize {
        self.compaction_keep_recent_tokens
    }
}

pub(super) fn load(config: &ComputerConfig) -> Result<Option<BuiltinProviderConfig>> {
    let Some(builtin) = &config.builtin else {
        return Ok(None);
    };
    ensure!(
        matches!(builtin.api_base.scheme(), "http" | "https"),
        "Builtin api_base must use http or https"
    );
    ensure!(
        builtin.api_base.username().is_empty() && builtin.api_base.password().is_none(),
        "Builtin api_base must not contain credentials"
    );
    ensure!(
        builtin.api_base.query().is_none() && builtin.api_base.fragment().is_none(),
        "Builtin api_base must not contain a query or fragment"
    );
    ensure!(
        !builtin.token.expose().trim().is_empty(),
        "Builtin token must not be empty"
    );
    ensure!(
        !builtin.model.trim().is_empty(),
        "Builtin model must not be empty"
    );

    Ok(Some(BuiltinProviderConfig {
        model: builtin.model.trim().to_owned(),
        api_base: builtin
            .api_base
            .to_string()
            .trim_end_matches('/')
            .to_owned(),
        token: builtin.token.clone_secret(),
        compaction_trigger_tokens: builtin.compaction_trigger_tokens(),
        compaction_keep_recent_tokens: builtin.compaction_keep_recent_tokens(),
    }))
}

#[cfg(test)]
mod tests {
    use url::Url;

    use super::*;
    use crate::config::{BuiltinOpenAiConfig, ConfigSecret};

    fn builtin(api_base: &str, token: &str, model: &str) -> ComputerConfig {
        ComputerConfig {
            builtin: Some(BuiltinOpenAiConfig {
                api_base: Url::parse(api_base).unwrap(),
                token: ConfigSecret::from(token),
                model: model.to_owned(),
                context_window_tokens: 128_000,
                compaction_trigger_ratio: 0.75,
                compaction_keep_recent_tokens: 20_000,
            }),
            ..ComputerConfig::default()
        }
    }

    #[test]
    fn loads_openai_compatible_provider_without_exposing_token() {
        let loaded = load(&builtin(
            "http://127.0.0.1:9/v1/",
            "not-for-logs",
            "test-model",
        ))
        .unwrap()
        .unwrap();

        assert_eq!(loaded.model, "test-model");
        assert_eq!(loaded.api_base, "http://127.0.0.1:9/v1");
        assert_eq!(loaded.compaction_trigger_tokens(), 96_000);
        assert_eq!(loaded.compaction_keep_recent_tokens(), 20_000);
        assert!(!format!("{:?}", loaded.token).contains("not-for-logs"));
    }

    #[test]
    fn loads_model_window_based_compaction_trigger() {
        let mut config = builtin("http://127.0.0.1:9/v1/", "not-for-logs", "test-model");
        let builtin = config.builtin.as_mut().unwrap();
        builtin.context_window_tokens = 16_000;
        builtin.compaction_trigger_ratio = 0.75;
        builtin.compaction_keep_recent_tokens = 8_000;

        let loaded = load(&config).unwrap().unwrap();

        assert_eq!(loaded.compaction_trigger_tokens(), 12_000);
        assert_eq!(loaded.compaction_keep_recent_tokens(), 8_000);
    }
}
