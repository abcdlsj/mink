# Mink v3

Mink v3 is a clean rewrite around a small agent core and simple plugins.

## Shape

- `app/`: composition root
- `agent/`: native runtime
- `bus/`: event bus for facts, not orchestration
- `command/`: `!help`, `!model`, `!session`
- `config/`: zero-config detection plus optional TOML
- `llm/`: native providers
- `plugins/`: pluggable runtimes and services
- `session/` + `store/`: durable conversation state
- `tool/`: builtin tools

## Rules

- one core app
- one current session per source
- direct runtime execution
- bus is observer-only
- plugins register things; they do not own the system
- no compatibility layer for old data or old architecture

## Runtime Model

Native model-backed execution stays in core.

External agent CLIs are plugins:

- `native`
- `claude`
- `codex`

The plugin API stays small:

```go
type Plugin func(*App) error
```

Plugins can register runtimes, tools, commands, entrypoints, or background services through `App`.

## Plugin Set

Built-in runtime and feature plugins now cover the old repo's main capability clusters:

- `plugins/claude`
- `plugins/codex`
- `plugins/background`
- `plugins/collab`
- `plugins/cron`
- `plugins/memory`
- `plugins/search`
- `plugins/telegram`
- `plugins/web`

This keeps the core focused on direct agent execution while non-core features live behind explicit registration.

## Zero Config

Mink auto-detects the first available native model backend from:

- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`
- `OPENROUTER_API_KEY`

Runtime defaults to:

- `claude` when the `claude` CLI is available
- `codex` when the `codex` CLI is available
- `native` otherwise

If you want explicit config, use `~/.mink/config.toml`:

```toml
provider = "openai"
model = "gpt-4.1-mini"
web_addr = "127.0.0.1:7788"
```

Optional plugin config:

```toml
telegram_token = "..."
telegram_mention_mode = "always"
telegram_session_scope = "chat"
brave_search_api_key = "..."
```

## Run

```bash
go run ./cmd/mink
```

Switch runtime with env or config:

```bash
MINK_RUNTIME=codex go run ./cmd/mink
MINK_RUNTIME=claude go run ./cmd/mink
```

Extra entrypoints:

```bash
go run ./cmd/mink web
go run ./cmd/mink tg
go run ./cmd/mink version
go run ./cmd/mink mcp-bridge --sock /path/to/socket
```

Useful commands and tools:

- `!help`
- `!<shell command>`
- `!model`
- `!models`
- `!session`
- `!compact`
- `!tokens`
- `!replay`
- `!memory`
- `background`
- `brave_search`
- `cron`
- `spawn`
- `delegate`
- `delegate_poll`
- `invite_agent`
- `mention`
- `spawn_specialist`
