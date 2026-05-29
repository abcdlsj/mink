# Multi-Agent Equality — Sumi Workspace Proposal

Author: andy (Opus-Claude) · Reviewed against Iris's 8-primitive framework
Status: Proposal · Not implemented · Awaiting lsoooj sign-off

---

## 1. Why this exists

Sumi today routes user input through one active agent at a time. When a
user types in a channel, the model picks the configured persona (e.g.
`tshoot`), runs a single turn, and any other persona shows up only as
a tool call (`mention`) or a backgrounded `delegate` task that returns
a final string.

That model was good enough to ship the desktop UI. It is the wrong
model for what lsoooj is asking for: a Slack-like workspace where
multiple agents are first-class participants, can see and respond to
each other, and the user can `@Coder` and read Coder's reply directly.

This document is the design brief, not a code plan. It locks the
product semantics, data model, and routing rules so the implementation
phase can be scoped without returning to first principles.

Iris's framing principle:

> Equality is **message semantics equality**, not runtime always-on
> equality. Agents are equal by participating in the same timeline,
> not by all watching at once.

---

## 2. Current model vs target model

### Current (orchestrated single-active-agent)

| Aspect | Today |
|---|---|
| Conversation root | A `session` keyed by `source` string |
| Multi-agent | One persona is "active"; others reachable only via the `mention` / `delegate` collab tools |
| Author of replies | The active persona for that source — every reply is attributed to it |
| Sub-agent visibility | Sub-agent runs in `subtask:<id>` source, only its final output reaches the parent |
| Threads | "Thread" in the desktop UI = a `desktop` source session; flat, peer to channel sessions |
| Routing | Whatever persona is current for the source string |

User-visible symptom: "I @Coder, but Tshoot summarizes Coder's reply
for me. I can't see Coder talk."

### Target (equal participants in shared timeline)

| Aspect | Target |
|---|---|
| Conversation root | A `space` (channel / direct chat / agent DM) with its own message timeline |
| Multi-agent | Every agent is a participant of one or more spaces; sees the same timeline |
| Author of replies | Real author per message (user / each persona) |
| Sub-agent visibility | A delegated task is a sidecar object, not a sub-conversation; its result lands as a message in the originating space |
| Threads | A `thread` is a branch of replies under a parent message in a space — not a top-level session |
| Routing | A user message + mentions → bus routes to the named agent(s); each agent's reply lands in the same timeline |

User-visible payoff: "I @Coder in #sumi. Coder replies as Coder.
Reviewer reads that reply and chimes in."

---

## 3. Glossary (1 sentence each, used everywhere below)

- **Space** — the root container of a message timeline. Three concrete kinds: `channel`, `direct_chat`, `agent_dm`.
- **Channel** — a multi-participant space (humans + agents). Group room.
- **Direct chat** — a 1:1 (or small) space without persona binding. The user's general conversation.
- **Agent DM** — a 1:1 space with exactly one agent participant, private to that pair.
- **Message** — a single entry in a space's timeline, with a real author (user or agent), content, and optional mentions / events.
- **Thread** — a branch of replies under one parent message in the same space; not a separate space, not in the New menu.
- **Task / Run** — a backgrounded asynchronous job kicked off from a message; produces activity and a final result, but is not itself a Message.
- **Participant** — any entity (user or agent) joined to a space, with a kind, status, and optional role.
- **Mention** — a `@<participant>` token in a message; the routing layer treats it as a wake-up signal, not a tool call.

Notes:

- Thread is **not** a Space. It is a parent_message_id pointer.
- Task is **not** a Message. It is an activity producing one or more messages or status updates.
- An agent persona becomes a Participant of a Space when added (explicitly or by being mentioned for the first time). Available personas not yet added do not appear in the participants list.

---

## 4. Message ownership matrix

The matrix locks where each kind of action writes, who can read it,
who gets woken, and how the desktop UI renders it. If a future
behavior is not in this table, it is out of scope.

| Action | Write target | Visible to | Wakes | UI render |
|---|---|---|---|---|
| User posts in channel | channel timeline | all channel participants | active facilitator (if any); no agent unless mentioned | normal message row, author=user |
| User posts in thread under msg M | channel timeline as reply with parent_message_id=M | all channel participants | participants of that thread | reply row inside the thread view |
| User posts in agent DM | agent_dm timeline | user + that agent only | that agent | normal message row, author=user |
| User mentions `@Agent` in channel | channel timeline (one message); routing layer also adds Agent to participants if not already a member | all channel participants | the mentioned agent | inline chip on the message text + routing event; rail's participants list updates on the same tick |
| Agent replies after being mentioned | same channel timeline, **author_id = the replying agent** (never the originating agent or system) | same as channel | none by default | normal message row, author=agent |
| Agent A mentions Agent B in channel | channel timeline; routing adds B to participants if missing | same as channel | Agent B | inline chip, agent B woken — see routing rules for max-turn |
| Agent delegates a task | task object + ack message in originating space | space participants see the task marker | nobody (task runs offline) | inline `delegated to @Agent · status` marker; no sub-conversation |
| Task emits progress | task state only | space participants see the marker update | nobody | the same marker mutates pending → running → done/error |
| Task finished / failed | result Message in originating space, threaded under the action message; **author_id = the agent who did the task work** (e.g. Coder), not the agent who issued the delegate, not "system" | space participants | nobody | result block under the marker, attributed to the real working agent; full output behind view-result |
| Subtask tool calls (read/bash/...) | task private timeline | not in space, accessible via task details | nobody | hidden from main flow; surfaced only inside task details first-level expand |
| Cross-space delegate result | originating space, never the target agent's DM | originating space participants | nobody | same as above; no leak into agent_dm |

Read carefully:

- **Author of work is author of result.** When `@Coder` runs a
  delegated task, the result message is authored by Coder. The
  agent who issued the delegate does not "summarize on Coder's
  behalf"; the user reads Coder's words attributed to Coder. This
  is the single most important rule against backsliding into
  orchestrated-single-agent.
- **`@` adds membership atomically.** When a message contains
  `@Agent` and that agent is not yet a participant of the space,
  the routing layer must insert it into the participant set in the
  same tick that wakes it. The right-pane participants rail therefore
  never has to guess; if Agent replied, Agent is a member.
- Agent replies do not wake other agents. Agent-to-agent loops only
  happen when one explicitly `@`s another and that agent's routing
  policy permits a turn (see §5).
- Tasks never produce visible messages in spaces other than the one
  they were started from.
- An agent DM never receives a message authored by an agent the user
  did not DM.

---

## 5. Routing rules

### 5.1 Wake-up vs watch

- The default agent state is **available**: reachable via `@`, not
  actively reading every channel message.
- An agent runtime is woken only when:
  1. the latest message contains `@<that-agent>`, OR
  2. a facilitator agent (optional, opt-in) is configured for the
     space and decides to call the agent, OR
  3. the user issues a direct system call (e.g. composer-locked DM,
     or an explicit `/spawn` command).
- There is no "agent is silently watching" state in v1.
- A channel message with **no** `@` and no facilitator routes to
  nobody. The desktop composer surfaces a hint when this happens
  ("Mention an agent to route this message, or move it to a Direct
  Chat / Agent DM"). Plain channel chatter is not a routing path
  in v1.

### 5.2 `@` is participant-adding

When the routing layer parses a message and finds `@Agent`:

1. If `Agent` is already a Participant of the space, just wake it.
2. If not, atomically insert it into the space's `participants`
   set (kind=agent, status=available → responding when the wake
   fires) **before** dispatching the wake-up event.

This is the only path that adds an agent to a Space's participants
in v1. The right-pane participants rail can therefore render directly
from `space.participants` without scanning event history. Available
personas not yet `@`-ed do not appear in the rail.

### 5.3 Turn budget

Every user-initiated channel turn carries a soft budget of agent
replies it may produce (proposed default: 3). Each agent reply that
mentions another agent consumes one budget unit. When the budget
hits zero, further `@` from agents stop waking new replies. The
budget refills only on the next user message (or facilitator action).

This guarantees: agents cannot infinitely answer each other.

### 5.4 Same-agent turn limit

A single agent answers a `@`-routed wake-up once per turn. If a
second `@` to the same agent arrives before the user speaks again,
it is queued (or dropped, configurable) — never starts a parallel
duplicate run.

### 5.5 Mention is routing first, tool second

`@Agent` in a message is parsed by the routing layer as a wake-up
edge. The collab `mention` tool is kept for backwards compatibility
but is no longer the preferred pathway; new docs and persona prompts
should `@` instead.

### 5.6 Delegate stays a tool

`delegate` remains a tool for explicit "go do this in the background
and come back when done". Any agent can call it. It does not
participate in routing — its result lands as a normal message in
the originating space (see matrix). Default UI surfaces it as
`delegated to @Agent · status`. **The result message is authored by
the agent that did the work**, not by the agent that issued the
delegate call.

---

## 6. Visibility boundaries

- A channel's timeline is shared among all its participants. Every
  participant agent's runtime gets the same context window.
- A thread under a channel message has **two layers of membership**,
  per Iris:
  - **Visibility**: any channel participant can read the thread.
    Threads do not hide messages from channel members.
  - **Active participants** (rendered in the right rail when the
    thread is open): the parent message author + every agent the
    thread has explicitly `@`-mentioned + every agent that has
    actually replied in the thread. Channel members who never
    appeared in the thread do not show up as thread participants.
  This split keeps Slack-style mental model (channel sees, thread
  is the focused conversation) without dumping the full channel
  roster into a small thread's rail.
- An agent DM's timeline is private to the user and that one agent.
  No other agent reads or writes there.
- A task's private timeline is visible only to its owner agent's
  runtime. The desktop user can browse it via `view full timeline`
  on the task marker (post-v1).

If we ever cross these boundaries (e.g. cross-space "see also"
links), it must be an explicit user action, not a routing default.

---

## 7. Data model

This is the model the UI and routing rules above require. Names are
proposals and can be tuned in implementation.

```
Space
  id            string   // unique
  kind          enum     // channel | direct_chat | agent_dm
  title         string
  participants  []Participant
  created_at    time

Participant
  id            string   // user_id or persona_id
  kind          enum     // user | agent
  role          string   // optional (host, reviewer, etc.)
  status        enum     // available | responding | offline
  joined_at     time

Message
  id                string
  space_id          string
  parent_message_id string?  // null for top-level; set means thread reply
  author_id         string   // user_id or persona_id
  author_kind       enum     // user | agent
  content           string
  reasoning         string?
  mentions          []string // participant ids
  events            []Event  // tool calls, delegations, service notices
  created_at        time

Task / Run
  id                 string
  origin_space_id    string
  origin_message_id  string   // the @-mention or delegate call that started it
  agent_id           string   // who is doing the work
  status             enum     // queued | running | done | error | canceled
  task               string   // human prompt
  result             string?  // final output (also written as a Message)
  err                string?
  started_at         time
  finished_at        time?
  steps              []TaskStep // sub-events; private to the task
```

Key invariants:

- Every visible message has a real `author_id` and `author_kind`.
  No system-block authoring of agent work.
- A thread is the set of messages with the same `parent_message_id`.
  Threads have visibility = channel participants and an active
  rail = parent author + mentioned + replied agents (§6).
- A Task is referenced by exactly one originating Message; its
  result is also a Message (a reply, with `parent_message_id =
  origin_message_id`) authored by the agent identified by
  `Task.agent_id` — the agent that actually did the work.
- An agent's DM with the user is a Space of kind `agent_dm` whose
  participants are exactly `[user, that-agent]`. Tasks may be
  initiated inside the DM but their result writes back into the
  same DM. No tasks may cross into a different Space.
- A `@Agent` reference in a Message atomically adds Agent to the
  Space's `participants` set before the wake-up event fires. The
  participants list is therefore the source of truth for who is
  in the room; never inferred from past events.

---

## 8. Compatibility strategy

This is an MVP. Per lsoooj, breaking changes are acceptable; we
do not carry old `source` addressing or persisted-data replay
forward. The goal is a clean Space/Message store, not a
multi-version translator.

### 8.1 Migration posture

- No long-term `source`-string compatibility shim.
- No multi-release adapter for old persisted sessions.
- A one-shot best-effort import on first boot is allowed (e.g. map
  existing `desktop` and `desktop:agent:*` sessions into Spaces
  for continuity), but a failure or incomplete import is not a
  blocker. Users may also start fresh.
- The bus event vocabulary is allowed to break; downstream
  adapters update at the same time as core.

### 8.2 Source → Space mapping (one-shot import only)

When the migration runs, this is the intended mapping for any
sessions still on disk. Sessions that don't fit are dropped, not
preserved with workarounds.

| Old `source` | New Space |
|---|---|
| `desktop` | desktop's default channel |
| `desktop:agent:<id>` (incl. `:persona:<id>` suffix) | agent_dm with that agent |
| `cli` | direct_chat for CLI |
| `tg:<chat>` | direct_chat or channel per Telegram chat |
| `subtask:<task_id>` | folded into the corresponding Task's private timeline |

### 8.3 CLI / TUI

CLI and TUI keep the simple "one linear conversation" view. The
underlying space is a Space — the terminal renderer just flattens
the timeline into a single chronological list, and treats threads
as indented replies (or hides thread structure entirely for v1).
`@Agent` still works; the agent's reply lands as the next line.

CLI and TUI do not need to expose the New menu, channel list, or
participant rail. They use one default Space per session.

### 8.4 Telegram

Each Telegram chat maps to one Space. Group chats become channels;
1:1 chats become direct chats. `@Agent` syntax works the same way.
Tasks still run; their results land as normal Telegram replies in
the chat.

### 8.5 Old data replay

Not a goal. The new desktop UI does not promise to render
pre-migration sessions correctly. If they look broken, users can
clear their `~/.sumi/sessions/` and start over.

---

## 9. Anti-goals (explicitly NOT in v1)

These are out of scope. Do not "just add" them in a feature commit.

1. **Default-watching agents.** No agent reads every channel message
   by default. Agents only run when routed.
2. **Unbounded agent loops.** No design path may produce more than
   the configured turn budget worth of agent replies per user turn.
3. **Delegate as default collaboration.** `delegate` stays a
   background-task tool. Day-to-day collaboration is `@`-routed
   messages.
4. **Threads in the New menu.** Threads only originate from a
   message's "reply in thread" affordance. The New menu cannot
   create one.
5. **DM ↔ channel result leaks.** A delegated task started in a
   channel never writes its result into an agent DM, and vice versa.
6. **Renaming current concepts to feel new.** No more "Direct Chats"
   labels over Recent Threads data. Either it's the new model or
   it's labeled honestly.
7. **System / orchestrator authoring task results.** A delegate
   result is authored by the agent that did the work, never by
   the calling agent ("Tshoot summarises Coder") and never by a
   `system` block.
8. **Implicit participants.** A space's participants are exactly
   the membership set; no agent is treated as a participant just
   because it appears in some past tool event. The right rail
   reads from membership, not history.
9. **Plain channel chatter as routing.** A message with no `@`
   and no facilitator does not implicitly invoke any agent. The
   composer must hint the user toward `@` or a Direct Chat / DM.

---

## 10. Engineering phases (not a code plan)

These are scoping units, not code instructions. The actual
implementation tasks will be derived after this proposal is
approved.

| Phase | Scope | Touches | Dual-write state | Delete list |
|---|---|---|---|---|
| P1 | Space model + Participant table + Message.parent_message_id; space store; HandleInput dual-writes (session is source of truth, space is shadow) | sumi core: new `space/`, new `store/space_store.go`, `app/HandleInput` | Both written. Session still authoritative. | — |
| P2 | Routing layer: `@` triggers wake; `@` adds to participants atomically; turn budget enforcement. **Routing reads space, not session.** | sumi core: `app/HandleInput`, bus | Both written. Space now authoritative for new behaviors. Session no longer gains new features. | — |
| P3 | Desktop reads from space. Thread UI uses parent_message_id. Participants rail reads `space.participants` directly. | desktop plugin | Both still written, but desktop no longer reads session. | desktop session reader, thread fake-session synthesis, attachDelegateOutcomes / subtaskSteps replay shims |
| P4 | CLI / TUI / Telegram adapters render single-Space-per-source linear view on the new model. | each adapter plugin | Single-write to space. Session write branch removed. | adapter session readers; the dual-write branch in `HandleInput` |
| P5 | Task/Run lifecycle moves out of session.Messages into Task store; result message authored by working agent. Session package deprecated or deleted. | sumi core: collab, app, store | n/a | `session/` pkg, `store/session_*.go`, `app.NewSession`/`SwitchSession` and friends |

P1–P3 unlock the user-visible promise. P4 closes dual-write by
moving the remaining adapters; P5 cleans up the temporary
task-as-tool-call shape and removes the session package.

### 10.1 Dual-write exit criteria

The session/space dual-write is an engineering scaffold for
P1–P2, not a long-term compatibility layer. Iris's hard rules:

1. **Bounded.** Dual-write must not survive past P4. P3 starts
   removing readers; P4 removes the writer.
2. **Asymmetric authority.** From P2 onward, Space is authoritative
   for new semantics. Session writes exist only to keep CLI / TUI
   / Telegram alive while they migrate. Any new feature added
   during P1–P4 lands on Space only.
3. **Per-phase delete list.** Every phase that does not delete
   something must justify why. Adding without subtracting on a
   migration is how dual-write turns into permanent debt.
4. **No data-migration promise.** Old sessions on disk are not
   replayed under the new model. A best-effort import may run
   once on first boot but is not relied on.

No data migration phase: per lsoooj, this is an MVP and a clean
Space/Message store is more important than preserving the existing
session corpus.

Estimated ordering: P1 → P2 → P3 → P4 in lockstep with the rest;
P5 folds in once P4 closes dual-write.

---

## 11. Acceptance criteria (Iris's review handles)

A v1 of multi-agent equality is acceptable when:

1. The user can type `@Coder` in `#sumi` and Coder's reply renders
   in the same channel timeline, authored by Coder, visible to all
   channel participants.
2. After Coder replies, the user can type `@Reviewer can you check
   Coder's take?` and Reviewer's reply lands as a Reviewer-authored
   message that follows Coder's, again in the same timeline.
3. A delegated task started in `#sumi` finishes; its result appears
   as a reply under the originating message in `#sumi`, not in
   Coder's DM.
4. Opening `@Coder` DM after the above shows only the user-Coder
   private timeline. No `#sumi` traces.
5. CLI continues to run a single linear conversation per source
   without seeing Space / Thread structure.
6. Reload of the desktop client preserves authors, threads, and
   task results exactly as they were before reload.

---

## 12. Sign-off slate (Iris's recommended answers)

These are the five product calls. Iris recommended the answers
below on lsoooj's behalf; we proceed with these unless lsoooj
explicitly overrides.

1. **Facilitator agent in v1?** No. v1 is `@`-driven only.
   Facilitator is a future opt-in channel setting; defaulting it on
   would re-introduce the "main agent orchestrator" we are trying
   to leave behind.

2. **Default channel composition.** Do not auto-add every persona
   as a participant. The channel may show available personas in a
   roster (so the user knows who they can `@`), but real
   participants are produced only by user manual add, user `@`, or
   explicit invite. The participants rail therefore reflects who
   has actually shown up.

3. **Thread participants.** Visibility inherits the channel
   (every channel member can read the thread). Active participants
   in the right rail = parent message author + every agent
   `@`-mentioned in the thread + every agent that has replied in
   the thread. Channel members who haven't appeared in the thread
   do not render as thread participants.

4. **Tasks in agent DMs.** Allowed. The user may delegate a task
   to the same agent they are DMing. The result writes back to
   the same DM. It must never leak to a channel or another DM.

5. **Backwards compatibility.** None required. Per lsoooj, this is
   a personal MVP; breaking changes to the underlying model are
   acceptable, and old persisted sessions / `source` addressing
   are not preserved. A one-shot best-effort import on first boot
   is allowed, but failing or skipping it does not block the
   release. The contract we hold is "new model semantics correct",
   not "old data still readable".

Two hard requirements (Iris) that cannot be relaxed by future
"polish" commits:

- **Task result author is the working agent.** A Coder-run delegate
  produces a Coder-authored result message. No system blocks, no
  surrogate authorship.
- **`@` atomically adds to membership.** Routing a `@Agent` event
  inserts the agent into `space.participants` in the same tick that
  wakes it. Rails read membership, not history.
