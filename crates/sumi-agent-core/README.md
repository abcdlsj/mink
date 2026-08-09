# sumi-agent-core

Portable generic agent runtime. It runs an OpenAI-compatible chat stream
through an agent loop with sandboxed `read`, `write`, `edit`, and `bash`
tools, persists the provider transcript on disk, and exposes a plugin API for
extra tools.

The runtime owns **no prompts and no context policy**. Embeddings implement
`ContextStrategy` to decide how the transcript is projected into provider
messages and whether/when it is compacted; the core only calls
`prepare_turn` before each turn and `project` when building the request.
Shared context algorithms (`find_cut_point`, `estimate_messages`,
`file_operations_appendix`) are provided as utilities for the strategies.

The crate has no dependency on the Sumi server, computer daemon, or domain
model. It only needs:

- an OpenAI-compatible chat-completions endpoint;
- a sandbox: `sandbox-exec` on macOS or `bwrap` on Linux;
- an agent home layout created by the embedding application:
  `agents/<agent-id>/{workspace,memory,runs,drivers/builtin}`.

## Usage

```rust
use std::sync::Arc;
use sumi_agent_core::{
    AgentConfig, AgentRuntime, IdentityContext, ProviderConfig, SandboxConfig, TurnRequest,
};

let config = AgentConfig {
    computer_home: data_dir.into(),
    provider: ProviderConfig::openai(api_key, model.into())
        .with_base_url("https://api.openai.com/v1".into()),
    sandbox: SandboxConfig::default(),
    context: Arc::new(IdentityContext),
};
let mut agent = AgentRuntime::new(config, vec![plugin]);
let locator = agent.create_session(agent_id).await?;
agent
    .start_turn(
        run_id,
        &locator,
        TurnRequest {
            system_messages: vec![Message::system("Your system prompt".into())],
            user_message: "Your user message".into(),
            attachments: Vec::new(),
            blocked_tools: Default::default(),
            prompt_cache_key: "cache-key".into(),
            sandbox_environment: Default::default(),
        },
    )
    .await?;
```

`attachments` carries image payloads (base64 `data`) or image URLs into the
provider request; non-image files stay on disk and are referenced by path in
the turn input.

## Embeddings

- `sumi-builtin-agent`: Sumi Computer harness (collaboration contracts and
  token-budget compaction).
- `sumi-telegram-agent`: Telegram harness (tool-driven prompts and its own
  compaction policy).

## Plugins

Implement `AgentPlugin` to add system-prompt text, extra tools, and their
execution inside the agent loop. See the `sumi-telegram-agent` crate for a
complete conversation-channel plugin.
