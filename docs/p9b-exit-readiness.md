# P9b Exit Readiness — task accessory inline status under trigger message

## Goal

Let the user see "this task is still running" attached to the
message that triggered the worker, without spinning up a separate
panel. Background Tasks already has the full list and detail; P9b
adds a one-line status under the trigger so the trigger point
itself shows the work is happening.

This is *not* a streaming surface and not a logs console. P9b reads
only the task store. P9 streaming + P9b accessory together close the
"协作正在发生" loop.

## What P9b added

### Backend (P9b.0)

`MessageView.task_accessory *TaskAccessoryInfo`:

```
TaskAccessoryInfo {
  TaskID, WorkerID, WorkerDisplay,
  Status        // queued/running/finished/failed/canceled/no_output
  ShortOutcome  // failed/canceled only, <= 80 runes
  Terminal      // true for finished/failed/canceled/no_output
}
```

`spaceMessagesToView` now also runs `computeTaskAccessoryIndex(sp,
a)` which scans `app.Tasks().ListBySpace(spaceID)` once and indexes
the latest task per `TriggerMessageID`. AgentDM Spaces explicitly
skip the projection.

`GetThreadDetail` runs the same index and attaches accessories on
parent + replies, so ThreadView replies surface accessory too.

Picks the latest task per trigger by `CreatedAt`; older tasks with
the same trigger collapse out of the inline accessory but remain
visible in Background Tasks. Documented as a P9b simplification.

ShortOutcome only attached on failed/canceled. finished, queued,
running, no_output do NOT carry an outcome body — the
worker-authored Space message is the canonical reply surface.

### Frontend (P9b.1)

`TaskAccessoryRow` renders inline below the trigger message body
(below `ThreadSummaryRow`):

| Status | Display | Visual |
| --- | --- | --- |
| queued | `<Worker> · queued` | text-text-muted |
| running | `<Worker> · working...` | text-text-muted + running dot |
| finished | `<Worker> · finished` | text-text-faint, no dot |
| failed | `<Worker> · failed: <short>` | text-text-faint |
| canceled | `<Worker> · canceled[: short]` | text-text-faint |
| no_output | `<Worker> · finished with no output` | text-text-faint |

Terminal accessories stay rendered, just dimmed. No timer-based
auto-hide (Iris: per-frame visibility must come from a real
dismissed signal, not a local clock).

`MessageView.task_accessory` flows through both the channel main
timeline and the ThreadView replies array.

### Frontend (P9b.2)

`lifecycleEventInScope(ev, state)` decides whether a turn /
delegate lifecycle event should trigger an active-scope refetch:

  - Channel / DirectChat main: `space_id == activeChannel` and no
    `parent_message_id`.
  - ThreadView: `space_id == activeChannel` AND `parent_message_id`
    is either the thread root OR a reply id in
    `threadDetail.replies`. A delegate fired from another thread in
    the same channel does NOT cause ThreadView to refetch.
  - AgentDM: `space_id == detail.item.id`.

Triggered events: `turn.started`, `turn.finished`, `turn.error`,
`agent.delegate.finished`, `agent.delegate.failed`. Each fires at
most one `refetchActiveScope` call. The refetch reuses the P9.3
helper; only the surface the user is looking at is reloaded.

The lifecycle handler does NOT render UI rows. It only schedules a
projection refresh. Streaming chunks remain controlled by P9.1's
`streamingEventInScope`.

## Hard rules

| Rule | Where | Test / Note |
| --- | --- | --- |
| accessory data source = task store only | `computeTaskAccessoryIndex` calls `Tasks().ListBySpace`; nothing else | per-status projection tests |
| mention has no accessory | mention does not call `Tasks().Create` (P7.2) so it is structurally absent from the projection | covered by P7.2 mention tests + P9b's "non-trigger has none" test |
| no outcome body on finished / queued / running / no_output | `projectTaskAccessory` only attaches `ShortOutcome` for failed / canceled | `TestTaskAccessoryFinishedHasNoShortOutcome` |
| short_outcome capped at 80 runes | `shortAccessoryOutcome` | implicit |
| AgentDM never shows accessory | `threadKindSupported` skip in `spaceMessagesToView`; explicit AgentDM test | `TestTaskAccessoryAgentDMSpacesGetNone` |
| latest task wins per trigger | sort by `CreatedAt` in `computeTaskAccessoryIndex` | `TestTaskAccessoryPicksLatestPerTrigger` |
| ThreadView lifecycle refetch only for thread-scope tasks | `lifecycleEventInScope` checks parent_message_id against root + replies | manual code review |
| terminal stays visible, dimmed | `Terminal` field on projection + `text-text-faint` styling, no auto-hide | manual code review |
| accessory does not duplicate worker reply body | only `WorkerDisplay` and `ShortOutcome` (failed/canceled) come through; outcome is never the full Space message body | code review + projection test |

## Acceptance run

| Scenario | Result |
| --- | --- |
| delegate from channel root | "<Worker> · working..." appears under root within one refetch |
| delegate finishes | accessory shows "<Worker> · finished" dimmed; worker reply body lives in its own Space message |
| spawn (sync) | accessory under caller's agent message shows running -> finished |
| spawn_specialist with task | accessory under trigger; bind-only spawn_specialist has no accessory |
| invite_agent with task | accessory under trigger; bind-only invite has no accessory |
| mention | NO accessory anywhere (mention is not in task store) |
| @worker inside thread | accessory under the thread reply, not on channel root |
| channel main view, thread has running worker | thread_summary "1 worker running" already covers; channel root does not gain accessory unless a task was triggered directly on root |
| failed task | "<Worker> · failed: <short>" stays visible, dimmed |
| canceled task | "<Worker> · canceled[: short]" stays visible, dimmed |
| empty_output task | "<Worker> · finished with no output" stays visible, dimmed |

Verification:
  - go build ./... passes
  - go vet ./... passes
  - go test ./... 20 / 20 packages pass
  - frontend tsc --noEmit passes

Backend tests added in P9b.0:
  - `TestTaskAccessoryAttachedToTriggerMessage`
  - `TestTaskAccessoryNonTriggerMessageHasNone`
  - `TestTaskAccessoryTerminalStatesPersist` (six subtests)
  - `TestTaskAccessoryFailedCarriesShortOutcome`
  - `TestTaskAccessoryFinishedHasNoShortOutcome`
  - `TestTaskAccessoryPicksLatestPerTrigger`
  - `TestTaskAccessoryAgentDMSpacesGetNone`

## Defer until later

- accessory dismissed/visibility persistence — out of scope; P9b
  keeps terminal accessories permanently visible (dimmed).
- accessory click-through to the RunCard detail in Background Tasks
  — surface stays simple; users still navigate to right rail.
- progress bars / step ticker on accessory — P5 KeyStep is the
  canonical detail surface, accessory is a single status line.
- thread reply count badge on left rail.
- multi-Run retry UI (multiple runs per task already exist in the
  store; UI hasn't surfaced them).
- session writer retirement.
- TG persona DM.
