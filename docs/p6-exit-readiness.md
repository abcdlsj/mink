# P6 Exit Readiness — dual-write deleted, AgentDM direct write live

## What P6 changed

- `app/agent_dm_writer.go` introduced `resolveAgentDMPersonaID`,
  `appendAgentDMUserToSpace`, `appendAgentDMAssistantToSpace`. Strict
  persona resolver: source seed and explicit persona must agree;
  unknown persona is hard-rejected (P6.0).
- `app/input.go` AgentDM branch writes the user message to Space
  before any session / runtime work; failure short-circuits the turn
  (P6.1). Side fix: `cli:agent:<id>` seed-only path now correctly
  hands the persona to the runtime env (was P4.9 dropping the
  persona).
- `app/turn.go` captures session baseline before runtime and, after
  Save, persists the assembled assistant turn into the AgentDM
  Space as a single message; tool-only / silent turns write nothing
  (P6.2).
- `app/space_dualwrite.go` deleted; `Spaces()` / `Tasks()` accessors
  moved to `app/api.go`. Non-router non-AgentDM sources no longer
  write to Space (P6.3).

## What P6 did NOT touch

- `spawn` / `mention` / `spawn_specialist` / `invite_agent` collab
  tools still take the legacy in-memory path. Their results do not
  enter the task store and do not show in Background Tasks. P7
  candidate.
- Thread UI (parent_message_id-driven reply UI). P8 candidate.
- TG persona DM. Out of scope.
- Session writers used as runtime context (autocompact, /file,
  /session, /compact) keep running.

## Failure semantics

| Stage | Failure mode | Effect |
| --- | --- | --- |
| AgentDM resolver | unknown persona / seed-explicit conflict / non-AgentDM source | turn errors before runtime; no session write, no Space write |
| AgentDM user write | Space append failure | pre-run hard fail; runtime never starts; clean state |
| Runtime | runtime error | session.Save + Space write skipped per existing turn flow |
| AgentDM assistant write | Space append failure | post-run persistence failure; session has raw assistant message as runtime context only; Space has no visible agent message; turn returns error; UI must treat as turn failure |

The last row is the only place where session and Space disagree, and
the disagreement is bounded: session residue is runtime context, not
a visible message. Any UI surface that reads from Space (desktop,
cli, telegram) will not show the residue as an agent reply. Iris
sign-off: this is post-run persistence failure, not full pre-run
sync.

## Source -> writer matrix after P6

| Source pattern | Kind | Writer for user | Writer for agent |
| --- | --- | --- | --- |
| `desktop` | Channel | `interceptRoutedInput` | router fanout |
| `desktop:direct:<id>` | DirectChat | `interceptRoutedInput` | router fanout |
| `cli` | DirectChat | `interceptRoutedInput` | router fanout |
| `tg:dm:<chat>` | DirectChat | `interceptRoutedInput` | router fanout |
| `tg:channel:<chat>` | Channel | `interceptRoutedInput` | router fanout |
| `desktop:agent:<id>` | AgentDM | `appendAgentDMUserToSpace` | `appendAgentDMAssistantToSpace` |
| `cli:agent:<id>` | AgentDM | `appendAgentDMUserToSpace` | `appendAgentDMAssistantToSpace` |
| `subtask:*` | n/a | not Space | `runSpaceDelegate` writes worker reply with explicit author into the parent Space (P5.2) |
| `scratch:*` | n/a | not Space | not Space |
| anything else | DirectChat (defaulted) | nothing | nothing |

The last row was previously covered by dual-write. P6.3 retired that.
The only producer of such sources today is internal test code; no
user-facing surface depends on it.

## Still-running session writers

Unchanged from P5 (these are runtime context, not visible messages):

- `app/turn.go` `sessions.Save` after every legacy turn
- `app/input.go` `sessions.Current(sessionSource)` for runtime input
- `app/compact.go` autocompact rewriting session
- `app/feature_commands.go` `/file` appending to session
- `app/commands.go` `/session` / `/compact` builtins

P7+ may chip away at these once spawn / mention / specialist also
move off session.

## Acceptance run

| Scenario | Test | Status |
| --- | --- | --- |
| PC AgentDM single turn produces user(1)+agent(1) | `TestE2EAgentDMSingleTurnNoDoubleMessage` | pass |
| CLI seed and PC explicit share same AgentDM Space | `TestE2ECLISeedDrivesPCAndCLISharedAgentDMSpace` | pass |
| Close + reopen with same DataDir keeps history intact | `TestE2EAgentDMHistoryReopenSeesEverything` | pass |
| AgentDM multi-assistant turn assembles into 1 Space message | `TestAgentDMAssistantMultiMessageAssemblesIntoSingleSpaceMessage` | pass |
| AgentDM tool-only turn writes 0 Space messages | `TestAgentDMAssistantToolOnlyTurnDoesNotWriteSpaceMessage` | pass |
| Unknown persona -> turn rejected, runtime never runs | `TestAgentDMHandleInputRejectsUnknownPersonaWithoutTouchingSession` | pass |
| Source seed + explicit persona conflict -> reject | `TestAgentDMHandleInputAsRejectsConflictBetweenSourceAndExplicitPersona` | pass |
| `cli:agent:tshoot` seed reaches runtime env Persona | `TestAgentDMHandleInputCLISeedDrivesRuntimePersona` | pass |
| AgentDM legacy path still uses runtime | `TestAgentDMStillUsesLegacyActivePersona` | pass |
| go build ./... | n/a | pass |
| go vet ./... | n/a | pass |
| go test ./... 20 / 20 packages | n/a | pass |
| frontend tsc --noEmit | n/a | pass |
| sumi binary builds | `go build ./cmd/sumi` | pass |

## Defer until later

- P7: spawn / mention / spawn_specialist / invite_agent migrate to
  task store + worker-author hard rule.
- P8: thread UI on parent_message_id.
- P9: streaming chunk display under router fanout in CLI / desktop.
- TG persona DM out of scope.
