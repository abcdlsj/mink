# P8 Exit Readiness — thread UI on parent_message_id, no thread Spaces

## What P8 added

### Backend
- `space.RoutingChain.ParentMessageID` carries the thread root through
  the routing fanout so agent replies inherit the same parent as the
  user reply that woke them.
- `space.Router.RouteUserChannelMessageInThread(spaceID, content,
  parentMessageID)` writes the user message with ParentMessageID set
  and starts a chain bound to that root. Existing
  `RouteUserChannelMessage` is now a thin wrapper passing `""` so
  non-thread paths are byte-identical.
- `App.interceptRoutedInput` reads `command.ParentMessageFrom(ctx)`
  and forwards to the thread-aware router. `runChannelWake` reads
  `target.Chain.ParentMessageID` and stamps it on each agent reply
  draft so worker replies land with `ParentMessageID = root`.
- `command.WithParentMessage` / `command.ParentMessageFrom` is the
  ctx carrier for parent_message_id along the inputFlow ->
  interceptRoutedInput -> runChannelWake chain.
- `Backend.ListThreadsForSpace(spaceID)` returns `[]ThreadSummary`
  for Channel / DirectChat Spaces only. Roots without replies are
  excluded. Sorted by last_reply_time desc.
- `Backend.GetThreadDetail(spaceID, parentID)` returns a
  `ThreadDetail` with parent + replies + thread-scoped participants
  + thread-scoped recent_runs + active_worker_id + last_reply_time.
  Two distinct missing states:
    `NotFound = true`     - data does not exist
    `Unsupported = true`  + `UnsupportedHint = "Threads are not
                            supported in agent DMs"`
  AgentDM goes to Unsupported. Stale parent goes to NotFound. They
  are not the same state.
- HTTP routes: `GET /api/threads-for-space?space=<id>` and
  `GET /api/thread-detail?space=<id>&parent=<msg-id>`.
- `Backend.SendMessage` accepts `SendRequest.ParentMessageID`. When
  set:
  - the target Space must be Channel or DirectChat; AgentDM rejects
  - the parent must exist in that Space; missing rejects
  - if parent itself has a non-empty ParentMessageID, it gets
    normalized to that ParentMessageID (no-nesting hard rule)
  - the resolved root is forwarded via `command.WithParentMessage`
- `MessageView.thread_info` (root-side) and `is_thread_reply`
  (reply-side) populated by `spaceMessagesToView` for Channel /
  DirectChat Spaces.

### Frontend
- `ThreadSummary` and `ThreadDetail` types in `lib/types.ts`.
- `api.threadsForSpace(spaceId)` and `api.threadDetail(spaceId,
  parentId)` + `api.send(..., parentMessageID?)`.
- Store: `threadDetail`, `openThread(parentId)`, `closeThread()`.
  `openChannel` / `openAgent` clear `threadDetail` so the right rail
  scope follows the same state.
- `panes/CenterPane`:
  - `renderableMessage` skips `is_thread_reply` so the channel main
    timeline never duplicates a reply body.
  - `ThreadSummaryRow` renders below the root message: "<n> replies
    · last by <Display> · <relTime>" with a running dot when
    `has_running_worker`. Low-weight underline button, not a card.
  - `<ThreadView />` activates whenever `threadDetail` is non-null.
    Header has Back-to-#channel button. Root message renders inside
    a context block labeled "Root message · context only" so it is
    visually distinct from the user's editable composer (Iris #1).
    Empty / not_found / unsupported states each render a single
    muted line inside the same chrome.
  - Composer placeholder switches to "Reply to thread..." when the
    thread is active.
- `panes/RightPane`:
  - `inThread = threadDetail set && supported && found` flips the
    branch from channel-scope to thread-scope. Channel-view branch
    is skipped when inThread.
  - Thread-scope participants and recent_runs come from
    `threadDetail.participants` / `threadDetail.recent_runs` so the
    state, not component residue, drives the rail (Iris #2).

## What P8 did NOT touch

- AgentDM threads. AgentDM is product-level "no threads" and the
  send path rejects `parent_message_id` for KindAgentDM.
- Streaming chunk display under router fanout in CLI / desktop.
  P9 candidate.
- Session writer cleanup (autocompact / /file / /session / /compact).
  Still runtime context only; no visible duplication issue.
- TG persona DM remains out of scope.
- CLI / TG threads. Both render in linear mode; thread summary is
  not shown there. Worker reply still carries parentMessageID for
  data correctness; UI just does not surface it.

## Hard rules and where they are enforced

| Rule | Enforced in | Test |
| --- | --- | --- |
| Thread is not a Space; only Channel / DirectChat host threads | `threadKindSupported` (backend); `inThread` derivation (frontend) | `TestListThreadsForSpaceReturnsNothingForAgentDM`, `TestGetThreadDetailUnsupportedForAgentDM` |
| AgentDM thread send rejected | `Backend.SendMessage` | `TestSendMessageThreadParentRejectedForAgentDM` |
| Stale parent rejected | `Backend.SendMessage`, `GetThreadDetail` | `TestSendMessageThreadParentNotFoundReturnsError`, `TestGetThreadDetailNotFoundForUnknownParent` |
| Reply of reply normalized to root | `Backend.normalizeThreadParentID` | `TestSendMessageThreadReplyToReplyNormalizesToRoot` |
| Routing chain dedupe keyed by user reply id, not root | `RoutingChain.RootMessageID` (= written.ID) | `TestSendMessageThreadTwoSeparateAtMentionsBothFire` |
| Worker reply parent inherits root | `runChannelWake` reads chain.ParentMessageID | `TestSendMessageThreadAgentReplyInheritsRootParent` |
| Roots with 0 replies hidden from list | `groupRepliesByParent` filter | `TestListThreadsForSpaceSkipsRootsWithoutReplies` |
| Right rail scope follows state, not component | `inThread` boolean drives `participantsList` and `recent` selection | manual: open thread, switch back, observe chip |
| Channel main timeline does not duplicate reply bodies | `renderableMessage` filters `is_thread_reply` | n/a (visual; covered by P8.2 backend test marking is_thread_reply) |

## Acceptance run

| Scenario | Test | Status |
| --- | --- | --- |
| roots without replies do not enter list | `TestListThreadsForSpaceSkipsRootsWithoutReplies` | pass |
| root with replies surfaces reply_count + last_reply_author | `TestListThreadsForSpaceReturnsRootWithReplies` | pass |
| GetThreadDetail returns parent + replies + participants (root included) | `TestGetThreadDetailReturnsParentRepliesAndParticipants` | pass |
| unknown parent -> NotFound (not Unsupported) | `TestGetThreadDetailNotFoundForUnknownParent` | pass |
| AgentDM -> Unsupported with hint | `TestGetThreadDetailUnsupportedForAgentDM` | pass |
| AgentDM list is empty | `TestListThreadsForSpaceReturnsNothingForAgentDM` | pass |
| thread runs scope: root + reply triggers, nothing else | `TestThreadRunsScopeIncludesRootAndReplies` | pass |
| HasRunningWorker / ActiveWorkerID lit when running task | `TestThreadHasRunningWorkerWhenTaskRunning` | pass |
| spaceMessagesToView attaches thread_info, marks is_thread_reply | `TestSpaceMessagesToViewAttachesThreadInfoToRootHidesReplies` | pass |
| AgentDM rejects thread send | `TestSendMessageThreadParentRejectedForAgentDM` | pass |
| missing parent rejects | `TestSendMessageThreadParentNotFoundReturnsError` | pass |
| reply lands with root as parent | `TestSendMessageThreadReplyWritesReplyWithRootAsParent` | pass |
| reply of reply normalizes to root | `TestSendMessageThreadReplyToReplyNormalizesToRoot` | pass |
| agent reply inherits root parent through routing | `TestSendMessageThreadAgentReplyInheritsRootParent` | pass |
| two separate @mentions in same thread fire twice | `TestSendMessageThreadTwoSeparateAtMentionsBothFire` | pass |
| `go build ./...` | n/a | pass |
| `go vet ./...` | n/a | pass |
| `go test ./...` 20 / 20 packages | n/a | pass |
| frontend `tsc --noEmit` | n/a | pass |

## Defer until later

- P9: streaming chunk display under router fanout. Visible work
  during a worker turn shows up in CLI / desktop only after the turn
  completes; live-typing for fanned-out workers is the next big
  surface.
- AgentDM session writer retirement (autocompact, /file, /session,
  /compact) — runtime context only; not visible.
- Thread reply count badges on the channel left rail entry.
- Multi-Run per Task (retry / replay).
- TG persona DM. Out of scope.
