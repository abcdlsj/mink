# Mink TUI Design

## Goal

Replace the line-based CLI with a full-screen TUI that keeps runtime logic unchanged while making terminal interaction readable, inspectable, and safe for Unicode text.

## Principles

- Show only information that helps the user decide the next action.
- Keep assistant conversation primary and system detail secondary.
- Hide raw tool arguments and large outputs by default.
- Make detail inspectable on demand instead of always visible.
- Treat UTF-8 safety as infrastructure, not a one-off bug fix.

## Layout

- `StatusBar`
  - Runtime, model, cwd, session, busy state, inspector state.
- `Transcript`
  - User messages, assistant output, concise notices, concise tool summaries.
- `Inspector`
  - Details for the selected transcript item: raw tool args, full output, reasoning, raw event metadata.
- `Input`
  - Multi-line input using `tview.TextArea`.
- `Modal`
  - Tool approval requests, help, session picker, confirm dialogs.

## Event Model

Bus events do not render directly. They are normalized into UI items:

- `user`
- `assistant`
- `tool`
- `notice`
- `error`
- `approval`

This keeps runtime protocol and display policy separate.

## Display Policy

Default transcript content:

- User input
- Assistant text
- Approval requests
- Errors
- Tool summaries such as `Read file app/cli.go` or `Run bash git status`

Inspector-only content:

- Raw tool args JSON
- Full stdout/stderr
- Reasoning text
- Raw bus event details

## Text Safety

All user-facing truncation goes through shared rune-safe helpers.

Initial migration targets:

- `plugins/codex/driver.go`
- `plugins/claude/driver.go`
- `session/session.go`
- TUI preview rendering

## Migration Plan

1. Add shared text helpers.
2. Add TUI event formatter and view model.
3. Replace `runCLI` with TUI startup.
4. Route approvals through TUI modal.
5. Remove plain CLI-only rendering code.
