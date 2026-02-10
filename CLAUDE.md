# Agentic Go

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
main.go                 # Entry point
internal/
  ├── agent/            # ReAct loop - orchestrates LLM and tools
  ├── config/           # Configuration management
  ├── event/            # Event system for async communication
  ├── llm/              # LLM clients (anthropic, openai)
  ├── msg/              # Message model and session persistence
  ├── tui/              # Terminal UI (bubbletea)
  └── tool/             # Builtin tools (shell, file)
```

## Core Concepts

### Message
Unified message model with session persistence (jsonl format)

### Agent
ReAct loop: LLM → Tool Call → Execute → LLM → ... → Response

### Tool
Builtin tools:
- `shell` - Execute shell commands
- `file` - Read/write/list/search files

### TUI
Single terminal interface using bubbletea with:
- Streaming/non-streaming modes (Ctrl+S toggle)
- Session management (/new, /clear)
- Tool execution visualization

## Configuration

Config file at `~/.config/otter/config.toml`:

```toml
provider = "anthropic"
model = "claude-sonnet-4-20250514"
api_key = "your-api-key"
stream = false
max_steps = 100
```

Sessions are persisted to `~/.config/otter/sessions/*.jsonl`

## Git Commit

When AI tools commit code, use this format:

```bash
git commit --author="<ToolName> <ai@songjian.li>" -m "type: message

Co-authored-by: <ToolName> <ai@songjian.li>"
```

- `<ToolName>`: AI tool name, e.g. `Claude Code`, `OpenClaw`
- Email: always use `ai@songjian.li`

---

**Author's Code Style**: Extreme efficiency, rationality, and correctness. Perfect code-ism. Aesthetics is the first productivity.
