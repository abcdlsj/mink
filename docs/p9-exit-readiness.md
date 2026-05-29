# P9 Exit Readiness — scoped streaming progress, no log console

## What P9 added

### Backend (P9.0)

`bus.Event` and `agent.Turn` carry four new fields:

  - `SpaceID` — the parent Space the event belongs to. Always the
    user-facing Space (Channel, DirectChat, AgentDM), never the
    `scratch:wake:*` / `subtask:*` runtime container.
  - `ParentMessageID` — thread root id; empty for top-level.
  - `AgentID` — persona id of the worker producing this turn.
  - `StreamID` — stable correlation id; the same value flows through
    `turn.started` -> `turn.chunk` / `turn.reasoning` /
    `tool.call.*` -> `turn.finished` / `turn.error`.

`agent.turnSink.Publish` auto-stamps these from the active Turn so
runtime callsites stay short. Existing Source / SessionID
auto-stamping is unchanged.

Publishers wired:

  - `app/turn.go` AgentDM legacy path
  - `app/space_routing.go runChannelWake` router fanout
  - `plugins/collab runSpaceDelegate` (delegate / spawn /
    spawn_specialist / invite_agent path)
  - `plugins/collab runWorkerAsMention` mention path

`Backend.toBusEvent` forwards all four fields onto the JSON
`BusEvent` published over `/api/events`.

### Frontend (P9.1)

`StreamingTurn` carries `streamID` / `agentID` / `spaceID` /
`parentMessageID`.

`streamingEventInScope(ev, state)` is the only gate every streaming
event passes through:

  - ThreadView active: `ev.space_id == activeChannel` AND
    `ev.parent_message_id == threadDetail.parent_id`
  - Channel / DirectChat main: `ev.space_id == activeChannel` AND
    `ev.parent_message_id` is empty
  - AgentDM: `ev.space_id == detail.item.id`
  - Anything else dropped

Streaming events without `ev.stream_id` are dropped before the
filter even runs.

Source-string sniffing for streaming is gone. The remaining
`subtask:*` source guard only applies to non-streaming event types
(`delegate.*` etc) until they are migrated.

Real worker placeholder author (Iris hard rule):

  - `turn.started` without `ev.agent_id` -> no placeholder is
    created; nothing is rendered. "Sumi-by-default" disappeared.
  - With `ev.agent_id`, placeholder author and display name resolve
    from the persona registry first, then the agent list.

Multi-worker correlation:

  - `turn.chunk` / `turn.reasoning` only mutate the placeholder if
    `ev.stream_id == cur.streaming.streamID`. Two concurrent workers
    in the same Space write into two placeholders without crossing.

ThreadView vs main timeline placement:

  - ThreadView active: placeholder appended to
    `threadDetail.replies` only, never to `detail.messages`. The
    channel main timeline does not flash a duplicate (P8 invariant
    preserved through P9).
  - `streamingMessageUpdates` patches both detail.messages and
    threadDetail.replies if the placeholder is present in both.

### Frontend (P9.2)

`ToolLine` is a single status row:

  - "Running <tool>" while the call is in flight
  - "Ran <tool>" on success
  - "Failed <tool>" on error
  - elapsed time
  - no "view details", no args body, no output body, no err body

Store-level: `tool.call.started` no longer copies `ev.input` into
`EventBlock.args`. `tool.call.finished` and `.failed` no longer copy
`ev.output` or `ev.err` into the body. Frontend therefore cannot
display raw stdout / cmd / args even if a publisher sends them on
the bus. `Ran bash: <cmd>` is structurally impossible.

Reasoning unchanged: the existing `ReasoningPreface` already
collapses to a single line by default with explicit user-driven
expand. P9 keeps that behavior.

### Frontend (P9.3)

`turn.finished` (StreamID-correlated):

  - Removes the placeholder from `detail.messages` and from
    `threadDetail.replies` in one set.
  - Calls `refetchActiveScope`, which only reloads the surface the
    user is currently looking at:

      ThreadView active     -> `api.threadDetail`
      channel mainline      -> `api.channel`
      direct chat (legacy "thread" view) -> `api.directChat`
      AgentDM               -> `api.agentDM`

  Left rail lists, participants, tasks rail are not touched. No
  high-frequency UI flicker under streaming load.

`turn.error` (StreamID-correlated):

  - Placeholder is kept; an inline service_notice with status
    "error" is attached so the user sees what failed. Stale
    placeholders are swept on the next user action (open a different
    scope / send a new message).

## What P9 did NOT touch

  - Task accessory ("<worker> is working" hung off the trigger
    message). Deferred to P9b.
  - Thread reply count badge in left rail.
  - Multi-Run retry UI.
  - Session writer retirement.
  - TG persona DM.
  - Raw event console (explicitly out of scope per Iris).

## Hard rules

| Rule | Enforced in | Test |
| --- | --- | --- |
| Streaming events without StreamID dropped on channel/thread/direct | `applyEvent` early gate in store | covered by `streamingEventInScope` decision tree (manual: backend never publishes without StreamID; we have publisher tests) |
| Channel / DirectChat main timeline never carries thread reply streaming | `streamingEventInScope` requires empty parent_message_id for non-thread scopes | covered by scope decision tree |
| ThreadView only shows streaming for its root | `streamingEventInScope` requires `parent_message_id == root` | covered by scope decision tree |
| Two concurrent workers in same Space stay separate | `chunk` / `tool.*` filter on `ev.stream_id == cur.streaming.streamID`; cur.streaming holds at most one StreamID at a time | partial — current store keeps a single streaming pointer; multi-worker broader support is naturally handled by the StreamID gate (mismatched events are dropped) |
| Real worker author or no placeholder | `turn.started` early return when `ev.agent_id` empty | manual code review |
| Tool row never shows args / output / err body | Store does not populate, ToolLine does not render | manual code review |
| Active scope refetch only on convergence | `refetchActiveScope` switch | manual code review |
| Streaming and committed Space message do not double-render | placeholder removed in same set as refetch trigger | manual code review |

Backend acceptance tests (publisher metadata) carry over from P9.0:

  - app TestAgentDMTurnPublishesStreamMetadata
  - app TestChannelWakePublishesSpaceAndParentAndAgent
  - app TestThreadWakePublishesParentMessageID
  - collab TestRunSpaceDelegatePublishesStreamMetadata
  - collab TestRunWorkerAsMentionPublishesStreamMetadata

## Verification

  - `go build ./...` passes
  - `go vet ./...` passes
  - `go test ./...` 20 / 20 packages pass
  - frontend `tsc --noEmit` passes
  - `cmd/sumi` binary builds

## Defer until later

  - P9b: task accessory ("<worker> is working" inline status hung
    off the trigger message). Wired only for delegate / spawn /
    spawn_specialist / invite_agent — mention does not enter task
    store and stays purely streaming-driven. P7 invariant preserved.
  - thread reply count badge in left rail.
  - multi-Run retry UI; multiple Runs per Task already exist in the
    store (P5.1) but UI hasn't surfaced them.
  - AgentDM session writer retirement.
  - streaming chunk batching/throttling under high token rate (only
    if the existing per-token chunk pattern starts costing CPU; not
    currently observed).
  - TG persona DM (out of scope).
