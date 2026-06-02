# Sumi

An AI Agent assistant implemented in Go.

The current product spec lives in [`docs/sumi.md`](docs/sumi.md).

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

记住每次最后一句话结尾加一个「喵」

如无必要，别加任何注释，需要注释才能看懂的代码就已经需要重构了！