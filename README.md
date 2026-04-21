# Mink v3

Mink v3 is a clean rewrite around a small agent core and simple plugins.

## Shape

- `app/`: composition root
- `agent/`: native runtime
- `bus/`: event bus for facts, not orchestration
- `command/`: `!help`, `!model`, `!session`
- `config/`: zero-config detection plus structured TOML
- `llm/`: native providers
- `plugins/`: pluggable runtimes and services
- `session/` + `store/`: text-based conversation state and run logs
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
active_model = "main"
default_model = "main"
web_addr = "127.0.0.1:7788"

[api_keys]
OPENAI_API_KEY = "${OPENAI_API_KEY}"

[models.main]
provider = "openai"
model = "gpt-4.1-mini"
api_key = "OPENAI_API_KEY"
max_tokens = 8192
context_window = 128000
```

Optional plugin config:

```toml
data_dir = "~/.mink"
soul_path = "~/.mink/SOUL.md"

[compact]
auto = true
trigger_messages = 80
keep_recent_messages = 8
reserve_tokens = 2048

[telegram]
token = "..."
mention_mode = "always"
session_scope = "chat"

[brave_search]
api_key = "..."
```

Runtime activity is written under `runlog/` in `data_dir`, and `!replay` reads from that event log instead of reconstructing output from session messages.

Prompt composition is shared by `native`, `claude`, and `codex` runtimes:

- base runtime prompt
- workspace + session summary context
- `SOUL.md` from `soul_path` or `data_dir/SOUL.md`
- custom `prompt`
- Telegram-specific reply directives when the source is `telegram:*`

In Telegram mode, dangerous tool actions use inline approval with session or persistent allow rules. Assistant output also supports:

- `[[reply_to_current]]`
- `[[reply_to:<message_id>]]`
- `[[react:👍]]`
- `NO_REPLY`

## Run

```bash
go run ./cmd/mink
```

Build a local binary with version metadata:

```bash
make build
./bin/mink version
```

Install `mink` into `GOBIN` or `GOPATH/bin` with version metadata:

```bash
make install
mink version
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
