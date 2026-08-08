# sumi-builtin-agent

Portable builtin LLM agent runtime extracted from Sumi. It runs an
OpenAI-compatible chat stream through an agent loop with sandboxed `read`,
`write`, `edit`, and `bash` tools, persists provider sessions on disk, and
exposes a plugin API for external conversation channels.

The crate has no dependency on the Sumi server, computer daemon, or domain
model. It only needs:

- an OpenAI-compatible chat-completions endpoint;
- a sandbox: `sandbox-exec` on macOS or `bwrap` on Linux;
- an agent home layout created by the embedding application:
  `agents/<agent-id>/{workspace,memory,runs,drivers/builtin}`.

## Usage

```rust
use std::sync::Arc;
use sumi_builtin_agent::{
    AgentConfig, AgentRuntime, ProviderConfig, SandboxConfig, TurnRequest,
};

let config = AgentConfig {
    computer_home: data_dir.into(),
    provider: ProviderConfig::openai(api_key, model.into())
        .with_base_url("https://api.openai.com/v1".into()),
    sandbox: SandboxConfig::default(),
};
let mut agent = AgentRuntime::new(config, vec![plugin]);
let locator = agent.create_session(agent_id).await?;
agent
    .start_turn(
        run_id,
        &locator,
        TurnRequest {
            product_contract: product_contract.into(),
            driver_contract: driver_contract.into(),
            identity: identity.into(),
            role: role.into(),
            input: serde_json::json!({ "message": text }),
            content_hash: content_hash.into(),
            attachments: Vec::new(),
            blocked_tools: Default::default(),
            sandbox_environment: Default::default(),
        },
    )
    .await?;
```

`attachments` carries image payloads (base64 `data`) or image URLs into the
provider request; non-image files stay on disk and are referenced by path in
the turn input.

## Plugins

Implement `AgentPlugin` to add system-prompt text, extra tools, and their
execution inside the agent loop. See the `sumi-telegram-agent` crate for a
complete conversation-channel plugin.
