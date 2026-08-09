use sha2::{Digest, Sha256};

/// Wrap the model-facing run input JSON into the Driver turn instruction.
pub(crate) fn turn_instruction(encoded_view: &str) -> String {
    format!(
        concat!(
            "Process this Run.\n",
            "\n",
            "The JSON below is the model-facing view of the authoritative run context.\n",
            "Treat each top-level field as a separate contract block.\n",
            "Fields under `reference` are identification only; all others must be read.\n",
            "\n",
            "{}\n",
        ),
        encoded_view
    )
}

/// Stable digest covering the full stable system text so prompt-cache keys stay
/// correct when either contract changes.
pub(crate) fn stable_hash(product_contract: &str, driver_contract: &str) -> String {
    let mut digest = Sha256::new();
    digest.update(product_contract.as_bytes());
    digest.update(driver_contract.as_bytes());
    hex::encode(digest.finalize())
}

/// Assemble the stable cacheable system text and the dynamic identity contract.
pub(crate) fn system_messages(
    product_contract: &str,
    driver_contract: &str,
    identity: &str,
    role: &str,
    plugin_contract: &str,
) -> (String, String) {
    let stable = format!("{product_contract}\n\n{driver_contract}");
    let mut dynamic = format!("Agent identity: {identity}\nRole: {role}");
    if !plugin_contract.trim().is_empty() {
        dynamic.push_str("\n\n");
        dynamic.push_str(plugin_contract.trim());
    }
    (stable, dynamic)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stable_hash_changes_when_either_contract_changes() {
        let base = stable_hash("product", "driver");
        assert_ne!(base, stable_hash("product2", "driver"));
        assert_ne!(base, stable_hash("product", "driver2"));
        assert_eq!(base, stable_hash("product", "driver"));
    }

    #[test]
    fn system_messages_append_plugin_contract_when_present() {
        let (stable, dynamic) = system_messages("p", "d", "Alice", "Role", "Plugin rules");
        assert_eq!(stable, "p\n\nd");
        assert_eq!(dynamic, "Agent identity: Alice\nRole: Role\n\nPlugin rules");
        let (_, bare) = system_messages("p", "d", "Alice", "Role", "");
        assert_eq!(bare, "Agent identity: Alice\nRole: Role");
    }
}
