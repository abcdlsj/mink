# Mink

Minimal AI coding agent. Fast, elegant, extensible.

## Features

- **Multiple Interfaces**: CLI, TUI, Telegram Bot
- **LLM Providers**: OpenAI, Anthropic, OpenRouter
- **Built-in Tools**: bash, read, write, edit, spawn, background, cron, brave_search
- **Extensible**: Drop executables to `~/.mink/ext/` or add skills to `~/.mink/skills/`
- **Cron Jobs**: Schedule AI tasks
- **Self-Update**: Auto-update binary
- **Session Management**: Branch and switch conversations

## Install

```bash
go install github.com/abcdlsj/mink@latest
```

Or download from [Releases](https://github.com/abcdlsj/mink/releases).

## Usage

```bash
# Config
cp config.example.toml ~/.mink/config.toml
$EDITOR ~/.mink/config.toml

# Run
mink
```

## Telegram Bot

```bash
mink tg -t <bot_token>
```

Group chat features:
- `NO_REPLY` - stay silent
- `[[reply_to:<message_id>]]` - target reply
- `[[react:👍]]` - emoji reactions

## Commands

- `/new` - new session
- `/branch <name>` - create branch
- `/switch <id>` - switch session
- `/compact [summary]` - compact history

## Config

```toml
provider = "anthropic"
model = "claude-sonnet-4-20250514"
api_key = "sk-ant-..."
base_url = ""           # custom endpoint
telegram_token = ""
mode = "tui"            # tui or cli
```

Flags override config: `mink -p openai -m gpt-4o -k sk-...`

## Extensions

- `~/.mink/ext/` - executable tools
- `~/.mink/skills/` - skill definitions (see SKILL.md)
- `~/.mink/SOUL.md` - persona guidance

Auto-reload on file changes.

## License

MIT
