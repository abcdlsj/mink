# Mink

Mink is a lightweight AI coding agent for local workflows. It supports interactive CLI usage and Telegram Bot mode.

The design goal is simple: keep the core small, fast, and easy to extend. Instead of hiding everything behind a heavy framework, Mink focuses on a minimal runtime, straightforward configuration, and a few practical extension points such as skills, external tools, and background jobs.

## Install

```bash
go install github.com/abcdlsj/mink@latest
```

You can also download a binary from [Releases](https://github.com/abcdlsj/mink/releases).

## Quick Start

1. Create a config file:

```bash
mkdir -p ~/.mink
cp config.example.toml ~/.mink/config.toml
```

2. Edit `~/.mink/config.toml` and set at least your model and API key.

3. Start Mink:

```bash
mink
```

See `config.example.toml` for a full example.

## Commands

```bash
mink                # start interactive mode
mink tg             # start Telegram Bot mode
mink version        # show version
```

CLI builtin commands:

```bash
!help
!session list|current|new|switch <id>|fork
!compact [note]
!tokens
!tools
!models
!model <name>
!replay [count]
```

Override config from flags:

```bash
mink -p openai -m gpt-4o -k <api_key>
```

## Telegram

Config keys:

- `telegram_mention_mode = "always" | "smart" | "mention_only"`
- `telegram_session_scope = "chat" | "thread"`

Assistant directives:

- `[[reply_to_current]]`
- `[[reply_to:<message_id>]]`
- `[[react:👍]]`
- `NO_REPLY`

## Deploy (Telegram Bot)

Linux (systemd):

```bash
cp deploy/mink.service ~/.config/systemd/user/
systemctl --user enable --now mink
```

macOS (launchd):

```bash
cp deploy/com.mink.agent.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.mink.agent.plist
```

## Paths

- `~/.mink/config.toml` — main config
- `~/.mink/skills/` — custom skills
- `~/.mink/ext/` — external executable tools
- `~/.mink/SOUL.md` — extra behavior guidance

## License

MIT
