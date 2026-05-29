# P7 Exit Readiness — collab tools unified on Task store, legacy in-memory map deleted

## Final tool matrix

| Tool | Task store | Space message | Background Tasks | Tool return |
| --- | --- | --- | --- | --- |
| `delegate` | yes | yes (worker author) | yes | `"delegation accepted, task_id=<id>"` |
| `spawn` | yes | yes (worker author) | yes | `"spawn <status>, message=<8-char-id>, outcome=<short>"` — never the worker reply body |
| `spawn_specialist` (with task) | yes | yes (worker author) | yes | `"spawned <alias> backed by <runtime>, task_id=<id>"` |
| `spawn_specialist` (bind-only) | no | no | no | `"spawned <alias> backed by <runtime>"` |
| `mention` | **no** | yes (worker author) | **no** | `"mentioned <Display>, message=<8-char-id>"` — never the worker reply body |
| `invite_agent` (with task) | yes | yes (worker author) | yes | `"invited <alias> backed by <runtime>, task_id=<id>"` |
| `invite_agent` (bind-only) | no | no | no | `"invited <alias> backed by <runtime>"` |
| `delegate_poll` | reads | n/a | n/a | task `Outcome` (short) |
| `cancel_delegation` | reads / writes | n/a | n/a | `"cancel requested for <id>"` |

## What P7 added

- `manager.resolveCollabWorkerPersona(source, alias)` — five distinct
  error sentinels (missing / unknown / not-persona / not-found /
  empty) so users can tell why an alias was rejected.
- `manager.runWorkerAsTask` — async, creates Task + Run, writes Space
  worker message, surfaces in Background Tasks.
- `manager.runWorkerSync` — sync, blocks to terminal status, returns
  `(ResultMessageID, Outcome)`. Outcome capped at
  `task.MaxOutcomeLen` (200 runes). Timeout cancels the task and the
  late worker message does not land in the Space.
- `manager.runWorkerAsMention` — runs a turn, writes a worker-authored
  Space message, but does NOT create a Task (mention is conversation
  fanout, not background work). Empty assistant output is a hard
  error.
- `task.Manager.Cancel(id)` — marks Task `StatusCanceled` via Update.
- `manager.cancel` and `manager.wait` are task-store-only.

## What P7 deleted

- `manager.delegate` (legacy in-memory queue + goroutine path)
- `manager.runDelegation`
- `manager.spawn` and `manager.spawnWithChild`
  (subtask:* `HandleInputWithRuntime` path; replaced by
  `runWorkerSync` / `runSpaceDelegate`)
- `manager.newTask` / `manager.finishTask` / `manager.task` / `task`
  struct
- `manager.publishTask` / `manager.publishResultNotice`
- `manager.tasks` map, `manager.queue` channel, `errQueueFull`
- `manager.waitPersisted` (legacy runlog replay fallback)
- `taskType` / `errString` / `newID` helpers (unused)
- `TestWaitFallsBackToRunLog`, `TestDelegateQueueBacksPressure`,
  `TestDelegateCancel` (legacy-behavior tests)
- spawn / mention / delegate / invite_agent / spawn_specialist
  legacy fallback branches in `tools.go`

Net code removal: ~340 lines (tracked + tests + helpers).

## Worker-author hard rule

Every visible worker reply now carries `AuthorID = registered persona
id`. There is no path that writes to a Space with `AuthorID = ""`,
`"system"`, `"sumi"`, or the initiator. Tests cover:

- `TestResolveCollabWorkerPersonaRejectsAliasBoundToRuntimeOnly`
- `TestResolveCollabWorkerPersonaRejectsUnknownAlias`
- `TestRunWorkerAsTaskRejectsUnknownPersona`
- `TestRunWorkerAsMentionDoesNotCreateTask` (asserts task store is
  untouched)
- `TestSpawnToolRejectsWorkerNotPersona`
- `TestMentionToolRejectsAliasNotPersona`

## Spawn timeout = cancel

`spawn` is synchronous from the caller's perspective: the caller
agent waits for a result pointer + summary. If the worker exceeds
`Config.Collab.PollTimeoutMS`:

- ctx is canceled
- `task.Manager.Cancel` flips Task to `StatusCanceled`
- the goroutine running the worker turn exits or is interrupted on
  its next ctx check
- if the worker happened to finish writing a Space message before the
  cancel landed, that message stays (it is committed); but the task
  is marked canceled
- the `late!` regression test asserts the more critical case: when
  the runtime is still blocked at cancel time, no late worker message
  lands in the Space after the timeout

If the user wants "fire and forget, ok if it lands later" semantics,
that is `delegate`, not `spawn`. P7 does not change that.

## Mention non-task fanout

`mention` is reserved for "ask the agent something inline" semantics
- not "schedule a background unit of work". Implementation:

- runs the worker turn against a fresh `subtask:<uuid>` scratch
  session
- assembles assistant output
- writes one worker-authored message into the parent Space
- emits `bus.ServiceNotice` (not `bus.DelegateFinished`)
- never touches `task.Manager`

Test (`TestMentionToolWritesSpaceMessageButNoTask`) asserts
`Tasks().ListBySpace(parent) == 0` after a mention completes.

## Source -> writer matrix after P7

| Source pattern | Kind | Tool entry | Path |
| --- | --- | --- | --- |
| `desktop` / `cli` / `desktop:direct:*` / `tg:dm:*` / `tg:channel:*` | router-managed | router intercept | space router fanout (P3-P4) |
| `desktop:agent:*` / `cli:agent:*` | KindAgentDM | inputFlow | `appendAgentDMUserToSpace` + `appendAgentDMAssistantToSpace` (P6) |
| `subtask:*` | n/a | runtime context only | session-only; no Space write |
| `scratch:*` | n/a | runtime context only | session-only; no Space write |
| collab `delegate` | runs through `runWorkerAsTask` | yes | task store + Space worker message |
| collab `spawn` | runs through `runWorkerSync` | yes | task store + Space worker message |
| collab `mention` | runs through `runWorkerAsMention` | yes | Space worker message (no task) |
| collab `spawn_specialist` (with task) | runs through `runWorkerAsTask` | yes | task store + Space worker message |
| collab `invite_agent` (with task) | runs through `runWorkerAsTask` | yes | task store + Space worker message |
| collab bind-only (`invite_agent` / `spawn_specialist`) | n/a | n/a | teams.json file only; no Space, no task |

## Still-running session writers (unchanged)

Same as P6:

- `app/turn.go` `sessions.Save` after every legacy runtime turn
  (AgentDM and any non-router non-agent source)
- `app/input.go` `sessions.Current(sessionSource)` for runtime input
- `app/compact.go` autocompact rewriting session
- `app/feature_commands.go` `/file` appending to session
- `app/commands.go` `/session` / `/compact` builtins
- `manager.clone` (collab share_context) — runtime context only

Sessions remain the runtime context container; they do not produce
visible Space messages directly. Visible writes always pass through
one of the four explicit writers (router, AgentDM, runWorkerAsTask /
Sync / Mention).

## Acceptance run

| Scenario | Test | Status |
| --- | --- | --- |
| spawn returns pointer + outcome, never full body | `TestSpawnToolReturnsResultMessageIDNotFullBody` | pass |
| spawn writes exactly one worker Space message + one Task | `TestSpawnToolWritesSingleWorkerMessageInSpace` | pass |
| spawn timeout cancels task and prevents late Space write | `TestSpawnToolTimeoutCancelsAndDoesNotLandLateMessage` | pass |
| spawn rejects worker that is not a registered persona | `TestSpawnToolRejectsWorkerNotPersona` | pass |
| spawn share_context clones parent session into runtime turn | `TestSpawnToolShareContextClonesParentSession` | pass |
| mention writes Space, no Task | `TestMentionToolWritesSpaceMessageButNoTask` | pass |
| mention rejects alias bound to runtime only | `TestMentionToolRejectsAliasNotPersona` | pass |
| mention empty output rejects | `TestMentionToolEmptyAssistantOutputRejects` | pass |
| spawn_specialist bind-only: no Task | `TestSpawnSpecialistBindOnlyDoesNotCreateTask` | pass |
| spawn_specialist with task: Task in store | `TestSpawnSpecialistWithTaskCreatesTaskInStore` | pass |
| invite_agent bind-only: no Task | `TestInviteAgentBindOnlyDoesNotCreateTask` | pass |
| invite_agent with task: Task in store | `TestInviteAgentWithTaskCreatesTaskInStore` | pass |
| cancel marks task store entry canceled | `TestCancelToolMarksTaskStoreCanceled` | pass |
| cancel of unknown task errors | `TestCancelToolUnknownTaskReturnsError` | pass |
| wait reads task store first / only | `TestWaitReadsTaskStoreFirst`, `TestWaitReportsFailedTaskFromStore`, `TestWaitFallsBackToLegacyForNonTaskStoreID` | pass |
| alias resolver: 5 distinct errors | `TestResolveCollabWorkerPersona*` | pass |
| go build ./... | n/a | pass |
| go vet ./... | n/a | pass |
| go test ./... 20 / 20 packages | n/a | pass |
| sumi binary builds | `go build ./cmd/sumi` | pass |

## Defer until later

- P8: thread UI on `parent_message_id` (worker reply already carries
  `parentMessageID` since P5.2; UI just needs to render the chain).
- P9: streaming chunk display under router fanout in CLI / desktop.
- AgentDM session writer cleanup (compact, /file, /session, /compact)
  if a follow-on phase decides to retire session as runtime context.
- TG persona DM remains out of scope.
