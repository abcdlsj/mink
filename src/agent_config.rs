use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, utoipa::ToSchema)]
#[serde(deny_unknown_fields)]
pub(crate) struct AttentionConfig {
    pub(crate) dm_immediate: bool,
    pub(crate) mention_immediate: bool,
    pub(crate) ambient_enabled: bool,
    pub(crate) ambient_debounce_seconds: u16,
    pub(crate) ambient_max_wait_seconds: u16,
    pub(crate) max_retry_count: u8,
}

impl AttentionConfig {
    pub(crate) fn is_valid(&self) -> bool {
        self.dm_immediate
            && self.mention_immediate
            && (1..=60).contains(&self.ambient_debounce_seconds)
            && (5..=300).contains(&self.ambient_max_wait_seconds)
            && self.ambient_max_wait_seconds >= self.ambient_debounce_seconds
            && self.max_retry_count > 0
    }
}

impl Default for AttentionConfig {
    fn default() -> Self {
        Self {
            dm_immediate: true,
            mention_immediate: true,
            ambient_enabled: true,
            ambient_debounce_seconds: 5,
            ambient_max_wait_seconds: 30,
            max_retry_count: 3,
        }
    }
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, utoipa::ToSchema)]
#[serde(rename_all = "snake_case")]
pub(crate) enum SuspendMode {
    StopAfterCurrent,
    CancelNow,
}

#[cfg(test)]
mod tests {
    use super::AttentionConfig;

    #[test]
    fn attention_configuration_has_one_validated_default() {
        let default = AttentionConfig::default();
        assert!(default.is_valid());

        let invalid = AttentionConfig {
            ambient_max_wait_seconds: default.ambient_debounce_seconds - 1,
            ..default
        };
        assert!(!invalid.is_valid());
    }
}
