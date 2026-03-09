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
- **Daemon Mode**: launchd on macOS, systemd user service on Linux

## Install

```bash
go install github.com/abcdlsj/mink@latest
```

Or download from [Releases](https://github.com/abcdlsj/mink/releases).

## Usage

```bash
cp config.example.toml ~/.mink/config.toml
$EDITOR ~/.mink/config.toml
mink
```

## Daemon

### Install daemon service

```bash
make daemon-install
# or
./scripts/install-mink.sh install
```

### Common daemon operations

```bash
make daemon-status
make daemon-reload
make daemon-restart
make daemon-uninstall
```

### Upgrade daemon

**Local development build**: rebuild the current repo and hot-swap the running daemon.

```bash
mink devbuild
make daemon-devbuild
./scripts/install-mink.sh devbuild
```

**Release upgrade**: download the latest release and hot-upgrade the daemon. If the latest release version matches the current binary, it exits cleanly without doing anything.

```bash
mink upgrade
make daemon-upgrade
./scripts/install-mink.sh upgrade
```

### Platform notes

- **macOS**: uses `~/Library/LaunchAgents/com.mink.agent.plist`
- **Linux**: uses `~/.config/systemd/user/mink.service`
- **Linux boot login persistence**: run `loginctl enable-linger $USER` if needed

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
base_url = ""
telegram_token = ""
mode = "tui"
```

Flags override config: `mink -p openai -m gpt-4o -k sk-...`

## Extensions

- `~/.mink/ext/` - executable tools
- `~/.mink/skills/` - skill definitions (see SKILL.md)
- `~/.mink/SOUL.md` - persona guidance

## License

MIT
