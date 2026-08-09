use std::{path::PathBuf, time::Duration};

use anyhow::{Context, ensure};
use chrono_tz::Tz;
use secrecy::SecretString;

use crate::scheduler::parse_timezone;

pub const DEFAULT_PRODUCT_CONTRACT: &str = concat!(
    "You are chatting with a user through Telegram. Reply text is delivered automatically as a ",
    "plain-text message; keep replies concise and formatted for a mobile chat.\n",
    "Attachments in the run context are saved under the given workspace/... paths. Images may ",
    "also be visible to you directly when the model supports vision; otherwise tell the user ",
    "you cannot inspect the image and ask for a description.\n",
    "Use telegram.send_file or telegram.send_image to deliver files you produce; after sending, ",
    "briefly tell the user what was sent.\n",
    "Never put secrets in replies or sent files.",
);

pub const DEFAULT_DRIVER_CONTRACT: &str = concat!(
    "Builtin read/write/edit tools and the bash tool run from the agent home. Use paths like ",
    "workspace/<path> or memory/<path>. Shell writes are only allowed under workspace/ and $TMPDIR ",
    "(the runs/ directory); /tmp is denied.",
);

#[derive(Clone, Debug)]
pub struct Settings {
    pub telegram_token: SecretString,
    pub api_base: String,
    pub model: String,
    pub api_key: SecretString,
    pub agent_home: PathBuf,
    pub identity: String,
    pub role: String,
    pub product_contract: String,
    pub driver_contract: String,
    pub timezone: Tz,
    pub turn_timeout: Duration,
}

impl Settings {
    pub fn from_env() -> anyhow::Result<Self> {
        let api_base = env_or("SUMI_BUILTIN_API_BASE", "https://api.openai.com/v1")?;
        ensure!(
            api_base.starts_with("http://") || api_base.starts_with("https://"),
            "SUMI_BUILTIN_API_BASE must use http or https"
        );
        let agent_home = PathBuf::from(env_or("SUMI_AGENT_HOME", "data")?);
        let timeout_seconds = env_or("SUMI_TURN_TIMEOUT_SECONDS", "600")?
            .parse::<u64>()
            .context("SUMI_TURN_TIMEOUT_SECONDS must be a positive integer")?;
        ensure!(
            timeout_seconds > 0,
            "SUMI_TURN_TIMEOUT_SECONDS must be positive"
        );
        Ok(Self {
            telegram_token: SecretString::from(env("SUMI_TELEGRAM_TOKEN")?),
            api_base,
            model: env("SUMI_BUILTIN_MODEL")?,
            api_key: SecretString::from(env("SUMI_BUILTIN_API_KEY")?),
            agent_home,
            identity: env_or("SUMI_AGENT_IDENTITY", "Telegram Agent")?,
            role: env_or(
                "SUMI_AGENT_ROLE",
                "Help the user with their requests and file tasks.",
            )?,
            product_contract: env_or("SUMI_PRODUCT_CONTRACT", DEFAULT_PRODUCT_CONTRACT)?,
            driver_contract: env_or("SUMI_DRIVER_CONTRACT", DEFAULT_DRIVER_CONTRACT)?,
            timezone: parse_timezone(&env_or("SUMI_AGENT_TZ", "Asia/Shanghai")?)?,
            turn_timeout: Duration::from_secs(timeout_seconds),
        })
    }
}

fn env(name: &str) -> anyhow::Result<String> {
    std::env::var(name).with_context(|| format!("{name} is required"))
}

fn env_or(name: &str, default: &str) -> anyhow::Result<String> {
    Ok(std::env::var(name).unwrap_or_else(|_| default.to_owned()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_contracts_are_non_empty() {
        assert!(!DEFAULT_PRODUCT_CONTRACT.trim().is_empty());
        assert!(!DEFAULT_DRIVER_CONTRACT.trim().is_empty());
    }

    #[test]
    fn from_env_requires_token_model_and_key_without_exposing_them() {
        for key in [
            "SUMI_TELEGRAM_TOKEN",
            "SUMI_BUILTIN_MODEL",
            "SUMI_BUILTIN_API_KEY",
        ] {
            unsafe {
                std::env::remove_var(key);
            }
        }
        let error = Settings::from_env().unwrap_err();
        assert!(!error.to_string().contains("secret"));
    }

    #[test]
    fn secret_strings_redact_in_debug_output() {
        let settings = Settings {
            telegram_token: SecretString::from("tg-secret"),
            api_base: "https://api.openai.com/v1".into(),
            model: "test-model".into(),
            api_key: SecretString::from("provider-secret"),
            agent_home: PathBuf::from("/tmp/none"),
            identity: "Agent".into(),
            role: "Role".into(),
            product_contract: "p".into(),
            driver_contract: "d".into(),
            timezone: parse_timezone("Asia/Shanghai").unwrap(),
            turn_timeout: Duration::from_secs(1),
        };
        assert!(!format!("{:?}", settings.telegram_token).contains("tg-secret"));
        let _ = settings;
    }
}
