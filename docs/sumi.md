# Sumi

Sumi is a desktop multi-agent collaboration workspace.

The mental model: agents are co-workers in a Slack-like channel.
Users mention them, listen-mode agents pick up the slack on their
own keywords, threads carry sub-topics, and each user-to-agent DM
is a fresh independent chat instance. There is no leader, no
meeting flow, no coordinator.

This file is the current spec. Everything else is implementation.

## Spaces

Three Space kinds:

- **Channel** — long-lived shared room. Has a title, a participant
  list (one user + N agents), a message timeline, and a per-agent
  mode map. Created by the user.
- **Thread** — a sub-context inside a channel, anchored on a parent
  message id. Threads are not separate Spaces; they live as messages
  whose `ParentMessageID` points at another message in the same
  Space. Threads inherit the channel's agents and can locally
  override per-agent modes without leaking back.
- **AgentDM (Agent Chat)** — one-on-one chat with a single agent.
  Each `New → Message agent` mints a new instance with its own
  history and auto-derived title. Persona id is who the agent is;
  Space id is which conversation.

A fourth surface, **DirectChat**, is a multi-participant space the
user creates ad hoc; functionally a channel without a fixed name.

## Agent modes

Each (channel × persona) pair has one of:

- `mention_only` — only `@persona` wakes the agent. Default.
- `listen` — the agent participates without being mentioned, gated
  by the channel's listening rules.

Per-thread overrides (`Space.ThreadAgentModes`) can flip a mode
inside one thread without changing the channel default.

## Routing

Source forms used by the desktop, CLI, and Telegram surfaces:

| Source | Kind | Seed |
| --- | --- | --- |
| `desktop` / `cli` | Channel | `default` (legacy fallback) |
| `desktop:channel:<spaceID>` | Channel | spaceID |
| `desktop:direct:<spaceID>` | DirectChat | spaceID |
| `desktop:agent:<X>` | AgentDM | persona id (legacy) **or** Space id |
| `cli:agent:<personaID>` | AgentDM | persona id |
| `tg:dm:*` / `tg:channel:*` | DirectChat / Channel | full source |

`MapSource`, `SourceUsesRouter`, and `Manager.Resolve` must agree
for any new source form. `Resolve` is the only entry point that
turns a source string into a Space — LoadSpace when the seed is
space-id shaped (`^\d{8}-`), EnsureSpace by seed otherwise. The
AgentDM writer adds persona-registry validation on top before
calling `Resolve`; nothing else carries its own resolver.

When the user message has at least one `@` mention, the router
fans out to those agents under a chained budget (`DefaultRoutingBudget`,
3 wakes). When there is no mention, explicit `listen` modes on the
channel/thread pick candidates directly. One listening agent wakes.
Many listening agents publish `routing.listening_ambiguous` and the
composer hint nudges the user to mention explicitly. Zero listeners
publish `routing.channel.no_target`. Each maps to a transient
composer hint, never a timeline message.

## Wake context

A woken agent runs in a scratch session seeded with up to the last
30 Space messages (channel main line if no parent, or thread root +
replies otherwise). The woken agent's own prior replies are mapped
to `assistant`; peer agents' messages are mapped to `user` with a
`[<agentID>] ` prefix; human messages are mapped to `user` with a
`[user] ` prefix. The originating user message is already in the
Space when the wake fires, so it lands in the seed naturally; the
explicit `scratch.Add` is only a fallback when Space load fails.

## AgentDM lifecycle

`Backend.GetAgentDM(personaID)` opens or creates the default
one-per-agent DM using stable seed `<personaID>`. Default agent DMs
are listed under Direct Messages only after the user explicitly
creates or opens that DM, and use the agent display name as their UI
title.

`Backend.CreateAgentDM(personaID, title)` mints a named AgentDM Space
with seed `<personaID>-<uuid8>`. `Backend.ListAgentDMs` returns only
named Agent Chat instances sorted by UpdatedAt. `Backend.GetAgentDM`
also accepts a Space id to open a named instance.

For named chats without a manual title, after the first agent reply
lands, `MaybeAutoTitleAgentDM` runs asynchronously, derives a title
from the first substantive user message, writes it to `Space.Title`,
and publishes `bus.SpaceTitleChanged`. Low-info user openers leave
the title blank ("New chat" in the UI). Default DMs and manually
titled chats do not auto-title.

## UI primitives

- **Quick Create** modal (Cmd/Ctrl+T, with Cmd/Ctrl+N and
  Cmd/Ctrl+Shift+T as fallbacks). Creates Channel, Agent Chat, or
  Direct Message. The left rail's `New` button routes through the
  same modal so creation never forks.
- **Channel header gear** — lists agents joined to the channel,
  toggles their mode, and offers `Add agent…` typeahead against
  personas not yet in the channel. Newly added agents start
  `mention_only`.
- **Thread header gear** — same shape, but writes to
  `ThreadAgentModes` and shows `Inherited from channel.`
- **Message body** — `@persona` tokens render as accent chips when
  the persona is registered; otherwise plain text.
- **Continue in thread** — replies on a parent message render a
  pointer to the thread once `reply_count >= 2`; one reply is still
  reachable via `Open thread →`.
- **Composer hints** — empty-channel hint
  `Mention or add an agent to collaborate.`; routing hints render
  for 4 seconds when the backend publishes a routing notice.
- **Auto-reply hint** — under a listening agent's reply, a faint
  one-liner reads `<Display> joined from channel listening.`

## Verification flows (handoff contract)

1. `@coder` in a channel adds Coder to `Participants` and produces
   a Coder reply.
2. With `Coder=Listen`, "retry failed in build" produces a Coder
   reply with `auto_reply_reason` set.
3. With `Coder=Listen`, "ok" produces no reply and no system noise.
4. The second wake in a channel sees the first message in
   transcript context.
5. `SetThreadAgentMode(rootID, coder, listen)` makes Coder listen
   inside the thread even when the channel default is mention-only.
6. Two `CreateAgentDM("coder", "")` calls return distinct Space ids;
   messages in one do not appear in the other.
7. DirectChat send round-trips through `resolveRoutedSpace` LoadSpace
   without depending on `sp.Title`.

`go build / vet / test ./...` must be clean. `tsc --noEmit` and the
frontend build must be clean. Sumi binary listens on
`127.0.0.1:7799` by default.

## Out of scope

- LLM-graded listening.
- LLM-rewritten AgentDM titles.
- Presence-probe / `Ping agents` action.
- A self-coordinating multi-agent meeting object. Agent-to-agent
  discussion happens inside a thread under the existing routing
  budget.
- Per-thread participant *membership* (vs *mode* override).

## File map

App layer:
  `app/space_routing.go` — channel/direct intercept, wake context,
                          listening fan-out
  `app/agent_dm_writer.go` — AgentDM Space resolver
  `app/agent_dm_title.go` — rule-based AgentDM title
  `app/turn.go` — turn pipeline + AgentDM persistence

Space layer:
  `space/space.go` — Space + AgentModes + ThreadAgentModes
  `space/listen.go` — explicit listening mode selection
  `space/manager.go` — MapSource, EnsureSpace, AgentMode setters
  `space/source.go` — SourceUsesRouter
  `space/routing.go` — Router + RoutingTarget + RoutingNotice

Desktop:
  `plugins/desktop/backend.go` — REST endpoints + projections
  `plugins/desktop/threads.go` — ThreadDetail
  `plugins/desktop/frontend/src/lib/store.ts` — store
  `plugins/desktop/frontend/src/lib/api.ts` — REST bindings
  `plugins/desktop/frontend/src/panes/CenterPane.tsx`
                          — channel/thread views, AgentGear,
                            mention rendering
  `plugins/desktop/frontend/src/panes/LeftPane.tsx` — left rail
  `plugins/desktop/frontend/src/components/QuickCreate.tsx`
  `plugins/desktop/frontend/src/components/Mention.tsx`
