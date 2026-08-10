use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct ComputerRelease {
    pub(crate) version: String,
    pub(crate) protocol_version: u16,
    pub(crate) target: String,
    pub(crate) artifact: String,
    pub(crate) sha256: String,
}

pub(crate) fn current_target() -> &'static str {
    if cfg!(all(target_os = "macos", target_arch = "aarch64")) {
        "aarch64-apple-darwin"
    } else if cfg!(all(target_os = "macos", target_arch = "x86_64")) {
        "x86_64-apple-darwin"
    } else if cfg!(all(
        target_os = "linux",
        target_arch = "aarch64",
        target_env = "musl"
    )) {
        "aarch64-unknown-linux-musl"
    } else if cfg!(all(target_os = "linux", target_arch = "aarch64")) {
        "aarch64-unknown-linux-gnu"
    } else if cfg!(all(
        target_os = "linux",
        target_arch = "x86_64",
        target_env = "musl"
    )) {
        "x86_64-unknown-linux-musl"
    } else if cfg!(all(target_os = "linux", target_arch = "x86_64")) {
        "x86_64-unknown-linux-gnu"
    } else {
        "unsupported"
    }
}

#[cfg(test)]
mod tests {
    use super::ComputerRelease;

    #[test]
    fn manifest_shape_is_stable() {
        let release = ComputerRelease {
            version: "1.2.3".into(),
            protocol_version: 4,
            target: "aarch64-apple-darwin".into(),
            artifact: "sumi".into(),
            sha256: "abc".into(),
        };

        assert_eq!(
            serde_json::to_string(&release).unwrap(),
            r#"{"version":"1.2.3","protocol_version":4,"target":"aarch64-apple-darwin","artifact":"sumi","sha256":"abc"}"#
        );
    }
}
