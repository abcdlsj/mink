use anyhow::{Result, ensure};
use secrecy::SecretString;

use crate::config::ComputerConfig;

use super::provider::ProviderConfig;

#[derive(Clone)]
pub(super) struct BuiltinProviderConfig {
    model: String,
    api_base: String,
    token: SecretString,
}

impl BuiltinProviderConfig {
    pub(super) fn into_provider_config(self) -> ProviderConfig {
        ProviderConfig::openai(self.token, self.model).with_base_url(self.api_base)
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
    }))
}

#[cfg(test)]
mod tests {
    use url::Url;

    use super::super::{
        provider::{OpenAiProvider, Provider},
        types::{Chunk, Message},
    };
    use super::*;
    use crate::config::{BuiltinOpenAiConfig, ConfigSecret};

    fn builtin(api_base: &str, token: &str, model: &str) -> ComputerConfig {
        ComputerConfig {
            builtin: Some(BuiltinOpenAiConfig {
                api_base: Url::parse(api_base).unwrap(),
                token: ConfigSecret::from(token),
                model: model.to_owned(),
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
        assert!(!format!("{:?}", loaded.token).contains("not-for-logs"));
    }

    #[test]
    fn rejects_invalid_openai_compatible_configuration() {
        let error = load(&builtin("file:///tmp/provider", "token", "model"))
            .err()
            .unwrap();
        assert!(error.to_string().contains("http or https"));

        let error = load(&builtin("https://api.example.test/v1", "", "model"))
            .err()
            .unwrap();
        assert!(error.to_string().contains("token must not be empty"));

        let error = load(&builtin("https://api.example.test/v1", "token", ""))
            .err()
            .unwrap();
        assert!(error.to_string().contains("model must not be empty"));
    }

    #[tokio::test]
    #[ignore = "manually contacts the OpenAI-compatible provider selected by SUMI_BUILTIN_* variables"]
    async fn live_builtin_provider_smoke() {
        let config = builtin(
            &std::env::var("SUMI_BUILTIN_API_BASE").expect("SUMI_BUILTIN_API_BASE is required"),
            &std::env::var("SUMI_BUILTIN_TOKEN").expect("SUMI_BUILTIN_TOKEN is required"),
            &std::env::var("SUMI_BUILTIN_MODEL").expect("SUMI_BUILTIN_MODEL is required"),
        );
        let loaded = load(&config)
            .expect("Builtin configuration must load")
            .expect("Builtin configuration must be present");
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
