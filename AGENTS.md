# Mink

An AI Agent assistant implemented in Go.

## Core Philosophy

- **Aesthetics is Productivity**: Beautiful code is the first priority
- **Efficiency & Correctness**: Extreme efficiency, rationality, and correctness
- **Perfect Code-ism**: Pursuit of perfect code
- **Clean and Elegant**: Hack when you can, but make it beautiful

## Code Style

Rob Pike's style Go code, elevated:

- **Short naming**: `i`, `s *Session`
- **Use interface sparingly**: Only when polymorphism is needed
- **Composition over inheritance**
- **Handle errors immediately**: `if err != nil { return err }`
- **Avoid over-abstraction**: But don't sacrifice elegance
- **Small functions, fit on one screen**
- **Names are documentation**: Only comment complex algorithms
- **Aesthetics first**: If it looks ugly, refactor it

## Architecture

```
cmd/mink/               # CLI entry point
app.go                  # App orchestration
agent/                  # ReAct loop, dispatcher, supervisor, prompt builder
bus/                    # Pub-sub message bus for async communication
command/                # Command registry and routing
config/                 # Configuration management (TOML)
cron/                   # Cron scheduling system
hook/                   # Event hooks (before/after input, tool, assist)
llm/                    # LLM providers (anthropic, openai/openrouter)
msg/                    # Unified message model
platform/               # Platform adapters (CLI/TUI, Telegram)
session/                # Session persistence (JSON)
skill/                  # Skill discovery and loading (SKILL.md)
tool/                   # Builtin tools (bash, read, write, edit, spawn, background, cron, search)
```

## Core Concepts

### Agent
ReAct loop: LLM → Tool Call → Execute → LLM → ... → Response

### Bus
Pub-sub message bus decouples agent, platform, and session layers.

### Tool
Builtin tools: `bash`, `read`, `write`, `edit`, `spawn`, `background`, `cron`, `brave_search`

## Git Commit

When AI tools commit code, use this format:

```bash
git commit --author="<ToolName> <ai@songjian.li>" -m "type: message

Co-authored-by: <ToolName> <ai@songjian.li>"
```

- `<ToolName>`: AI tool name, e.g. `Claude Code`, `OpenClaw`, `Kimi`, `OpenCode`, `Cursor`, `AmpCode`, `GitHub Copilot`
- Email: always use `ai@songjian.li`

---

**Author's Code Style**: Extreme efficiency, rationality, and correctness. Perfect code-ism. Aesthetics is the first productivity.

## Attentions

记住只有最后一句话结尾加一个「喵」
