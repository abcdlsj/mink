# 🦦 Mink

Minimal AI coding agent. Fast, elegant, extensible.

## Philosophy

- **Aesthetics first**: Beautiful code is productive code
- **Minimal core**: 4 tools only (read, write, edit, bash)
- **Maximum extensibility**: Extensions and skills
- **Tree sessions**: Branch and compact

## Quick Start

```bash
export OPENAI_API_KEY=sk-...
./mink

# Or anthropic
export ANTHROPIC_API_KEY=sk-ant-...
./mink -p anthropic -m claude-sonnet-4-20250514

# With telegram
./mink -tg "YOUR_BOT_TOKEN"

# CLI mode
./mink -c
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
