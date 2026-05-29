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
| User mentions `@Agent` in channel | channel timeline (one message) | all channel participants | the mentioned agent | inline chip on the message text + routing event |
| Agent replies after being mentioned | same channel timeline | same as channel | none by default | normal message row, author=agent |
| Agent A mentions Agent B in channel | channel timeline | same as channel | Agent B | inline chip, agent B woken — see routing rules for max-turn |
| Agent delegates a task | task object + ack message in originating space | space participants see the task marker | nobody (task runs offline) | inline `delegated to @Agent · status` marker; no sub-conversation |
| Task emits progress | task state only | space participants see the marker update | nobody | the same marker mutates pending → running → done/error |
| Task finished / failed | result message in originating space (replying to the marker, threaded under the action message) | space participants | nobody | result block under the marker; full output behind view-result |
| Subtask tool calls (read/bash/...) | task private timeline | not in space, accessible via task details | nobody | hidden from main flow; surfaced only inside task details first-level expand |
| Cross-space delegate result | originating space, never the target agent's DM | originating space participants | nobody | same as above; no leak into agent_dm |

Read carefully:

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

### 5.2 Turn budget

Every user-initiated channel turn carries a soft budget of agent
replies it may produce (proposed default: 3). Each agent reply that
mentions another agent consumes one budget unit. When the budget
hits zero, further `@` from agents stop waking new replies. The
budget refills only on the next user message (or facilitator action).

This guarantees: agents cannot infinitely answer each other.

### 5.3 Same-agent turn limit

A single agent answers a `@`-routed wake-up once per turn. If a
second `@` to the same agent arrives before the user speaks again,
it is queued (or dropped, configurable) — never starts a parallel
duplicate run.

### 5.4 Mention is routing first, tool second

`@Agent` in a message is parsed by the routing layer as a wake-up
edge. The collab `mention` tool is kept for backwards compatibility
but is no longer the preferred pathway; new docs and persona prompts
should `@` instead.

### 5.5 Delegate stays a tool

`delegate` remains a tool for explicit "go do this in the background
and come back when done". Any agent can call it. It does not
participate in routing — its result lands as a normal message in
the originating space (see matrix). Default UI surfaces it as
`delegated to @Agent · status`.

---

## 6. Visibility boundaries

- A channel's timeline is shared among all its participants. Every
  participant agent's runtime gets the same context window.
- A thread under a channel message inherits the channel's
  participants by default; explicit add/remove is allowed but not
  required for v1.
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
- A thread is the set of messages with the same `parent_message_id`.
- A Task is referenced by exactly one originating Message; its
  result is also a Message (a reply, with `parent_message_id =
  origin_message_id`).
- An agent's DM with the user is a Space of kind `agent_dm` whose
  participants are exactly `[user, that-agent]`. No tasks may
  cross into it from elsewhere.

---

## 8. Compatibility strategy

The current sumi model has a single `session` keyed by a `source`
string. The migration strategy must not break CLI / TUI / Telegram.

### 8.1 Mapping existing sessions to spaces

| Current `source` | Target Space |
|---|---|
| `desktop` | desktop's default channel (one channel for the workspace) |
| `desktop:agent:<id>` (incl. `:persona:<id>` suffix) | agent_dm with that agent |
| `cli` | direct_chat for CLI |
| `tg:<chat>` | direct_chat per Telegram chat (or channel if multi-user) |
| `subtask:<task_id>` | the Task's private timeline (not a Space) |

A migration function reads the existing session by source and writes
its messages into the new model with derived `author_id` (using the
`personaFromSource` rule we already use in desktop replay).

### 8.2 CLI / TUI

CLI and TUI keep the simple "one linear conversation" view. The
underlying space is still a Space — the terminal renderer just
flattens the timeline into a single chronological list, and treats
threads as "indented replies" (or hides thread structure entirely
for v1). `@Agent` still works; the agent's reply lands as the next
line.

CLI does not need to expose the New menu, channel list, or
participant rail. It just uses one default Space per source.

### 8.3 Telegram

Telegram is naturally chat-bound. Each Telegram chat maps to one
Space. Group chats become channels (multi-participant). `@Agent`
syntax works the same way. Tasks still run; their results land as
normal Telegram replies in the chat.

### 8.4 Replay

Old sessions on disk continue to render. Desktop already has the
`attachDelegateOutcomes` / `subtaskSteps` correlator; in the new
model the same data is stored as Messages + Tasks, so the replay
layer collapses into "load Space messages" with no special-casing.

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

---

## 10. Engineering phases (not a code plan)

These are scoping units, not code instructions. The actual
implementation tasks will be derived after this proposal is
approved.

| Phase | Scope | Touches |
|---|---|---|
| P1 | Space model + Participant table + Message.parent_message_id | sumi core: `session/` → `space/`, store schema |
| P2 | Routing layer: `@` triggers wake; turn budget enforcement | sumi core: `app/HandleInput`, bus |
| P3 | Migrate desktop to spaces; New menu maps to space creation; thread UI uses parent_message_id | desktop plugin |
| P4 | CLI / TUI / Telegram adapters use single-Space-per-source compatibility shim | each adapter plugin |
| P5 | Task/Run lifecycle moves out of session.Messages into Task store | sumi core: collab, app, store |
| P6 | Old data migration on first boot | sumi core: store migration |

P1–P3 unlock the user-visible promise. P4 keeps every other
adapter working. P5 cleans up the temporary task-as-tool-call shape.
P6 makes existing users not lose history.

Estimated ordering: P1 → P2 → P3 → P4 in lockstep with the rest;
P5 and P6 can fold in once P1 lands.

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

## 12. Open questions for sign-off

1. **Facilitator agent role**: should we ship one in v1 (e.g. a
   `host` persona that routes new user messages to the right
   specialist) or leave routing fully `@`-driven?
2. **Default channel composition**: when sumi spins up a new
   workspace, do we auto-add every configured persona to the
   default channel, or do we require the user to add them via
   `@` first?
3. **Thread participants**: do threads inherit the channel's
   participants, or start with just the parent message's
   `[user, mentioned-agents]` and expand on `@`?
4. **Tasks across DMs**: should a task started in an agent DM be
   allowed at all, or is it channel-only? (My read: allow, but
   the result still lands only in that DM.)
5. **Backwards compat window**: do we need to keep the old `source`
   string addressing on the bus for one release so external
   integrations can migrate?

These do not block the proposal; they need lsoooj's product call.
