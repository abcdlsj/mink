# 🦦 Mink

Minimal AI coding agent. Fast, elegant, extensible.

## Philosophy

- **Aesthetics first**: Beautiful code is productive code
- **Minimal core**: 4 tools only (read, write, edit, bash)
- **Maximum extensibility**: Extensions and skills
- **Tree sessions**: Branch and compact

## Quick Start

```bash
# 1. build
go build -o mink

# 2. config
cp config.example.toml ~/.mink/config.toml
# edit ~/.mink/config.toml

# 3. run
./mink
```

## Config

`~/.mink/config.toml`:

```toml
provider = "anthropic"
model = "claude-sonnet-4-20250514"
api_key = "sk-..."
base_url = ""           # custom endpoint
telegram_token = ""     # optional
mode = "tui"            # tui or cli

[headers]
User-Agent = "custom"
```

Flags override config:
```bash
./mink -p openai -m gpt-4o -k sk-... -c
```

## Commands

- `/new` - new session
- `/branch <name>` - create branch
- `/switch <id>` - switch session
- `/compact [summary]` - compact history

## Extensions

Drop executables to `~/.mink/ext/` or skills to `~/.mink/skills/`.

Auto-reload on change.

## Code Style

Short names, small functions, composition over inheritance.

No over-abstraction. Aesthetics first.

## License

MIT
