# P5 Exit Readiness — Task Store, dual-write, session dependency status

## What P5 added

- `task/` package + `store/task_store.go` file backend (P5.1).
  - `Task{ID, SpaceID, TriggerMessageID, InitiatorID, WorkerID, Title,
    Status, Outcome, ResultMessageID, Source, CreatedAt, UpdatedAt}` —
    no body field; `Outcome <= 200`.
  - `Run{ID, TaskID, StartedAt, EndedAt, Status, KeySteps[]}` —
    KeySteps title <= 80, kind whitelist {read, write, run, subtask,
    summary, error}, max 8 entries.
- `app.App.Tasks() *task.Manager` accessor.
- collab `delegate` tool for Space-anchored sources writes through
  `task.Manager` and produces exactly one worker-authored Space
  message via `space.Manager.AppendAgentMessage` (P5.2 / P5.2.1).
- `manager.wait` consults the task store first then falls back to the
  legacy in-memory map / runlog replay (P5.3).
- Desktop right-rail Background Tasks reads tasks from the store by
  SpaceID, exposes `GET /api/run?id=` for a metadata-only RunDetail,
  and the RunCard expands inline to show Outcome / ResultMessageID
  pointer / KeySteps; "no_output" surfaces as "Finished with no
  output" (P5.3 / P5.3.1).
- CLI `cli` and `cli --persona <id>` resolve a stable default persona
  via `Config.DefaultPersona` -> sorted-first persona; mismatched
  config emits a stderr warning instead of silently changing the
  default (P5.0 / P5.0.1).

## What P5 did NOT touch (intentional)

- AgentDM (`cli:agent:*`, `desktop:agent:*`) still runs through the
  legacy `runTurnAs` -> `sessions.Save` path. dual-write keeps that
  Space content in sync.
- `spawn`, `mention`, `spawn_specialist` collab tools still take the
  legacy in-memory path. Only the user-facing `delegate` tool was
  re-routed through the task store.
- Thread UI (`parent_message_id` first-class threads) and full real-
  time chunk streaming under router fanout remain deferred.
- TG persona DM remains out of scope.

## Still-running session writers

| Path | Why kept | Migration trigger |
| --- | --- | --- |
| `app/turn.go:31` `sessions.Save` after every legacy turn | AgentDM / non-routed runtime context | AgentDM migrates to direct Space write |
| `app/input.go:99` `sessions.Current(sessionSource)` | runtime context fetch for AgentDM | AgentDM migrates |
| `app/compact.go:65` auto-compact session rewrite | runtime context maintenance | AgentDM migrates |
| `app/feature_commands.go:91` `/file` appends to session | runtime context | AgentDM migrates |
| `app/commands.go:81-116` `/session` / `/compact` builtins | user-facing session commands | session UI replacement |

## Still-running dual-writers

These mirror legacy AgentDM activity into Space so the desktop /
cli Space-driven render keeps working. They only fire for non-routed
sources; routed sources skip dual-write entirely (router writes
Space directly), and `subtask:*` is hard-skipped at the top of every
dual-write helper.

| Path | Skip behavior | Status |
| --- | --- | --- |
| `app/space_dualwrite.go:29` `dualWriteUserInput` | early return on `subtask:*`; not called when source is router-managed | KEEP — only writer of AgentDM Space user messages |
| `app/space_dualwrite.go:58` `dualWriteAssistantMessage` | early return on `subtask:*` and empty `personaID` | KEEP — only writer of AgentDM Space assistant messages |
| `app/space_dualwrite.go:88` `dualWriteAssistantsFromSession` | called from `app/turn.go:36` only | KEEP — keeps AgentDM Space in sync per turn |
| `app/turn.go:36` call site | active for legacy runs | KEEP |
| `app/input.go:97` call site | active for legacy non-routed user input | KEEP |

Conclusion: dual-write **cannot be deleted in P5**. AgentDM still needs
it. Per the P5 plan v2 commitment, this is a readiness statement, not
a demolition.

## Still-running session readers

| Path | Role | Notes |
| --- | --- | --- |
| `cli/shell_session.go` | session selector overlay + history switch | unchanged, drives `/session` UI |
| `cli/shell_session.go:146` `loadTranscript(s)` | render fallback for non-Space-mapped sources only | unchanged, normal sources go through `loadSpaceTranscript` |
| `cli/shell_model.go:1291` | header chip session id label | cosmetic only |
| `plugins/desktop/backend.go:129` | `SwitchSession` from desktop UI | bound to legacy session selector |
| `plugins/sessioncmd/*` | `/session` / `/inspect` / `/replay` commands | debug / introspection only |

## Still-running legacy delegate paths

| Tool | Path | Enters task store? | Enters Background Tasks? |
| --- | --- | --- | --- |
| `delegate` | `tryDelegateInSpace` -> `delegateAsync` -> `runSpaceDelegate` when source has Space anchor; legacy `manager.delegate` otherwise | yes for space-anchored only | yes for space-anchored only |
| `delegate` (no Space anchor) | legacy `manager.delegate` | no | no |
| `spawn` | `manager.spawn` -> `manager.spawnWithChild` (legacy, synchronous) | no | no |
| `mention` | `manager.delegate` (legacy async, in-memory map) | no | no |
| `spawn_specialist` | `manager.delegate` (legacy async) | no | no |
| `invite_agent` | `manager.delegate` (legacy async, optional) | no | no |
| `manager.clone(parent, child)` | `app.NewSession(child)` + `app.SaveSession(dst)` for share-context spawn | n/a — runtime context, not Space | KEEP until spawn/mention/specialist migrate |

Migration trigger for these: an explicit P6 to push `spawn` /
`mention` / `spawn_specialist` through `task.Manager`. Until then,
their results do not show up in Background Tasks and are not subject
to the worker-author hard rule.

## Pre-existing test debt acknowledged at P5 start

| Test | Status | Notes |
| --- | --- | --- |
| `plugins/sessioncmd.TestReplayCommandUsesRunLog` | green at P5 entry | fixed in P4.2 follow-up commit `90ecc1f` |
| `plugins/sessioncmd.TestInspectCommandShowsSnapshotAndRunLog` | green at P5 entry | same fix |

No test debt accumulated by P5.

## P5 exit gate (this branch)

| Check | Result |
| --- | --- |
| `go build ./...` | passes |
| `go vet ./...` | passes |
| `go test ./...` | 20 / 20 packages pass, no FAIL |
| `tsc --noEmit` (frontend) | passes |
| Unknown worker on space-anchored delegate | rejected (no task created in store, no entry in legacy map) |
| Single worker message per delegate | enforced + tested |
| Worker AuthorID = configured worker | enforced + tested |
| KeyStep titles never carry tool stdout / reasoning | enforced + tested |
| `Outcome` capped at 200 runes | enforced + tested |
| Task accessed by SpaceID, not source | enforced (`ListTasksBySpace`) |
| RunDetail hides result body | enforced (no Content / Reasoning fields surfaced) |
| EmptyOutput surfaces as user-facing string | "Finished with no output" |
| `delegate_poll` returns short outcome / status / result_message_id | confirmed; `Outcome <= 200` invariant unchanged |

## Defer until later

- AgentDM migration to direct Space write -> deletes dual-write,
  shrinks session writers (P6 candidate).
- spawn / mention / spawn_specialist task store migration (P6+).
- Thread UI (parent_message_id) and reply count (P6+).
- Run history beyond the latest run (multi-Run per Task with retry)
  (P7+).
- Streaming chunk display under router fanout in CLI / desktop
  (P7+).
- TG persona DM (out of scope per project decision).
