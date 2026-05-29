# P4 Exit Readiness — Dual-Write & Session Dependencies

This document records every place the legacy session writer / reader still
runs after P4 ships, so P5 can pick up the deletion plan with full
visibility. Nothing here is a recommendation to delete during P4.

## Still-running session writers (authoritative writes)

These call sites continue to write into the legacy `session.Manager` and
must keep doing so until P5 migrates the underlying capability:

- `app/turn.go:31` — `f.app.sessions.Save(f.session)` after every legacy
  runtime turn (Channel/DirectChat run via the router and bypass turnFlow,
  so they do not hit this path; AgentDM and unrouted runs do).
- `app/input.go:99` — `sessions.Current(sessionSource)` to fetch the
  legacy session that AgentDM and unrouted runtimes consume.
- `app/compact.go:65` — auto-compact rewrites session messages for
  AgentDM / non-routed sources.
- `app/feature_commands.go:91` — `/file` command appends to the active
  session.
- `app/commands.go:81-116` — `/session` and `/compact` built-ins.

## Still-running dual-writers (shadow writes)

These mirror legacy session activity into Space and exist only because
the legacy path is still authoritative for AgentDM / non-routed runs:

- `app/turn.go:36` — `dualWriteAssistantsFromSession` after every
  successful legacy turn.
- `app/input.go:97` — `dualWriteUserInput` for non-routed sources.
- `app/space_dualwrite.go` — entire file is shadow-write helpers.

These can be deleted as soon as AgentDM and other non-routed paths run
through Space directly (P5).

## Still-running session readers (legacy session inputs to UI)

- `cli/shell_session.go` — session selector / switch overlay; transcript
  load for sources that do not map to a Space (subtask/scratch).
- `cli/shell_model.go:1291` — header chip uses session id label.
- `plugins/desktop/backend.go:129` — `SwitchSession` from desktop UI.
- `plugins/sessioncmd/*` — `/session` / `/inspect` / `/replay` commands.

These can shrink once Task/delegate (P5) and Space-based session selector
(P5+) replace them.

## Adapter render paths

- `plugins/desktop/backend.go` — fully Space-driven for Channel /
  DirectChat / AgentDM (P3 + P3.6).
- `cli` — Space-driven render for every source whose `MapSource` returns
  a Kind. Legacy `loadTranscript(session.Session)` only fires for sources
  that do not map (subtask/scratch).
- `plugins/telegram` — Space-driven reply formatting (`Display: body`)
  for every Space-mapped source; legacy `latestAssistant` reply path
  retained as the fallback for non-mapped sources.

## P4 exit gate

| Surface | Space-driven | Legacy still active | Notes |
| --- | --- | --- | --- |
| desktop | yes | session selector, SwitchSession | legacy is selector-only, not transcript |
| cli/tui | yes | session selector, transcript fallback for non-mapped sources | hint + author prefix for routed sources |
| telegram | yes | only for sources outside `MapSource` (none today) | one reply per agent |
| dual-write | running | full | deletion deferred to P5 per Iris |

P4 is intentionally a "Space-compatible linear mode" — agent identity,
fanout, and history rendering are correct; full real-time work surface
(streamed chunk display under router fanout, thread UI, task store) is
P5 / later.
