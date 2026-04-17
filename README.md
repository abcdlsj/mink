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

## Zero Config

Mink auto-detects the first available backend from:

- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`
- `OPENROUTER_API_KEY`
- local Ollama at `http://127.0.0.1:11434`

If you want explicit config, use `~/.mink/config.toml`:

```toml
runtime = "native"
provider = "openai"
model = "gpt-4.1-mini"
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
