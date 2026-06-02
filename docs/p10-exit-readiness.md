# P10 Exit Readiness — Channel-as-collaboration, multi-instance Agent Chat, listening, context injection

## Goal

Sumi's collaboration mental model finally lands.

A Channel is a long-lived shared room with explicit agent
participants. Each agent has a per-channel and per-thread mode
(mention-only or listening). Threads are sub-topic spaces inside
the channel that inherit but can override agent modes locally. A
direct message with an agent is no longer a singleton per persona;
each `New → Message agent` produces a fresh Agent Chat instance
with its own history and auto-generated title. When an agent gets
woken in a channel or thread it now sees up to the last 30 Space
messages, not just the originating user line.

This phase closes Iris's "agent 是 channel 同事" mental model — not
a `HuddleRun`, not a coordinator, not a meeting flow.

## What P10 added

### Channel agent participants (P10.A)

- `Space.AgentModes map[personaID]string` (channel-level)
- `Backend.AddAgentToChannel`, `Backend.SetChannelAgentMode`
- POST `/api/channel/add-agent`, POST `/api/channel/agent-mode`
- ChannelItem now carries `agent_modes`; `agents` already projected
  the participants list.
- ListeningGear in CenterPane shows only joined agents; "Add agent…"
  opens a typeahead picker against personas not yet in the channel.
- Newly added agents default to `mention_only`. User toggles
  Listen explicitly.
- Channel header subtitle: `3 agents · Coder listening · Reviewer mention only`,
  collapses to `+N` past the second agent.
- Empty-state composer hint: `Mention or add an agent to collaborate.`

### Thread per-agent override (P10.B)

- `Space.ThreadAgentModes map[parentMessageID]map[personaID]string`
- `space.ListeningAgentsForThread(sp, parentMessageID)` merges
  channel modes with thread overrides; thread overrides win.
- `space.Manager.SetThreadAgentMode`
- `Backend.SetThreadAgentMode` + POST `/api/thread/agent-mode`
- `ThreadDetail` now exposes `channel_agents` and `agent_modes`
  (effective merged).
- ThreadGear in ThreadView header lets the user flip Listen/Mention
  inside the thread without touching channel config; subtitle
  reads `Inherited from channel.`
- Routing layer consults `ListeningAgentsForThread` when
  `parent_message_id` is set on a no-mention reply.

### Multi-instance Agent Chat (P10.C)

- AgentDM Spaces are no longer singleton-per-persona. Each
  `Backend.CreateAgentDM(personaID)` mints a new Space (seed =
  `<personaID>-<uuid8>`) with the persona joined.
- `desktop:agent:<X>` source now accepts both legacy persona id
  and a Space id; `agentDMSpaceIDPattern` (`^\d{8}-`) disambiguates.
- `Backend.ListAgentDMs` replaces persona list in left rail "Agent DMs".
- Async `MaybeAutoTitleAgentDM` derives a short title from the first
  substantive user message after the first agent reply lands; locks
  on success so titles do not flap. "hi", "在吗", "ok" etc. are
  filtered as low-info.

### Channel context injection (P10.D)

`runChannelWake` previously seeded its scratch session with the
single originating user line. The runtime literally said "我这边
没有之前的上下文". Now `seedWakeContext` loads up to the last
`wakeContextLimit = 30` Space messages, scoped to either the
channel main line (parent_message_id == "") or a specific thread
(root + replies), and maps each to a runtime msg.Message with the
woken agent's own prior replies as `assistant`, peer agents as
`user` with `[<agentID>] ` prefix, and human messages as `user`
with `[user] ` prefix.

### Source/Space addressing invariants (P10.E)

Three places must agree for any new source form:
1. `space.MapSource` — emit Kind + Seed
2. `space.SourceUsesRouter` — opt the form into the route intercept
3. `Ensure/Load` callers — `EnsureForSource` and
   `app.resolveRoutedSpace` LoadSpace-by-id when seed is Space-shaped,
   else EnsureSpace-by-seed

P10 added `desktop:channel:<spaceID>`. SendMessage now uses Space id
for all three Kinds (Channel, DirectChat, AgentDM), avoiding the
`Title`-as-seed drift that bit channel "11".

### Quick Create modal (P10.F)

Single Cmd/Ctrl+T (with Cmd/Ctrl+N + Cmd/Ctrl+Shift+T fallbacks)
opens a centered modal with three options: Channel, Agent Chat,
Direct Message. Channel takes a name input; Agent Chat opens an
agent typeahead and creates a fresh Agent Chat instance; Direct
Message creates a new direct chat. The legacy `New` button in the
left rail also routes through this modal so creation paths do not
fork.

### Mention highlight (P10.G)

`@<personaID>` tokens in user input and agent reply Markdown render
as accent-colored chips when the id is a known agent. Unknown
mentions stay as plain text to avoid implying clickability.

### Channel listening gate (P10.H)

No-mention channel messages run a rule-based listening gate (no LLM
call):
- Low-info filter (length + denylist) skips greetings.
- Per-persona keyword bucket: `coder` matches `code/build/retry/...`,
  `reviewer` matches `review/regression/risk/...`, `tshoot` matches
  `debug/trace/panic/...`. Keywords are deliberately a small const
  table; not user-configurable in this phase.
- 1 hit → wake that agent with `AutoReplyReason: "joined from channel listening"`.
  The reply renders with a `<Coder> joined from channel listening.` hint above.
- 0 hits + listening agents present → frontend hint
  `No listening agent matched this. Mention one explicitly.`
- 0 hits + no listening agent → frontend hint
  `No agent picked this up. Mention an agent or enable listening.`
- ≥2 hits → frontend hint `Mention a specific agent.`
  No agent fires; user disambiguates.

## Verification flows

Run by manual + stub-runtime smoke. The smoke driver was removed
after the phase per AGENTS.md no-test rule, but the seven flows it
exercised stand as the readiness contract:

1. Channel `@coder` adds Coder as participant and produces a Coder
   reply.
2. Channel listening with Coder=Listen + keyword "retry failed in
   build" produces a Coder reply with `auto_reply_reason` set.
3. Channel listening with low-info "ok" produces no agent reply
   and no system noise.
4. Channel wake on the second message can see the first message in
   transcript context (no "上下文是空的" symptom).
5. Thread override: `SetThreadAgentMode(rootID, coder, listen)`
   makes Coder listen inside the thread even when the channel
   default is mention-only.
6. Agent Chat `desktop:agent:<spaceID>` writes the user message
   into the addressed Space and produces a reply; two instances
   of the same persona stay isolated.
7. DirectChat send with `desktop:direct:<spaceID>` round-trips
   through `resolveRoutedSpace.LoadSpace-by-id`, no Title drift.

`go build / vet / test 20/20` clean. `tsc --noEmit` clean. Frontend
build clean. sumi binary running on `127.0.0.1:7799`.

## Known boundaries (intentional, not in this phase)

- LLM-based listening gate: gates are rule-only. A noisy false
  positive or a missed match is observable; Iris and lsoooj agreed
  to ship rule-first and revisit if behavior diverges.
- LLM-based AgentDM title: derivation is rule-based truncation.
  Auto-rewrite to a smarter title is deferred.
- "Ping agents" / presence-probe button: removed from the spec.
  Users should `@` an agent or enable Listen.
- Multi-agent self-coordination ("HuddleRun"): explicitly rejected.
  Agent-to-agent discussion happens inside a thread, bounded by
  the existing routing chain budget; no leader, no coordinator.
- Per-thread *participant* membership (vs per-thread *mode*):
  threads inherit the channel's participant list and only override
  modes. Adding/removing agents per-thread is not supported in
  this phase.
- Visual polish (subtitle chip, listening status density,
  presence dot animation): Iris parked these as nice-to-haves.

## Files touched in this phase

App / routing / data layer:
  app/space_routing.go (intercept + wake context seeding)
  app/agent_dm_writer.go (Space-id resolver for agent_dm)
  app/agent_dm_title.go (rule-based AgentDM title)
  app/turn.go (turn flow keeps the streaming/save/space-write
               sequencing; AgentDM persistence path goes through
               persistAssistantTurn)
  space/space.go (AgentModes + ThreadAgentModes)
  space/listen.go (rule gate + ListeningAgentsForThread)
  space/manager.go (SetAgentMode, SetThreadAgentMode,
                    AddAgentParticipant, EnsureForSource Space-id
                    LoadSpace fast path, MapSource desktop:channel:)
  space/source.go (SourceUsesRouter desktop:channel:)
  space/routing.go (no-mention listening branch + ListeningNoMatch
                    + ListeningAmbiguous notice kinds)

Desktop backend:
  plugins/desktop/backend.go (channel/thread agent-mode +
                              add-agent endpoints, GetChannel
                              Space-id LoadSpace, ListAgentDMs,
                              CreateAgentDM)
  plugins/desktop/threads.go (ThreadDetail effective modes +
                              channel agents)
  plugins/desktop/mock.go (ChannelItem.AgentModes + AgentDMItem)

Frontend:
  plugins/desktop/frontend/src/lib/types.ts
  plugins/desktop/frontend/src/lib/api.ts
  plugins/desktop/frontend/src/lib/store.ts (composerHint,
                                             quickCreateOpen,
                                             setChannelAgentMode,
                                             addAgentToChannel,
                                             setThreadAgentMode,
                                             newAgentChat, agentDMs)
  plugins/desktop/frontend/src/components/QuickCreate.tsx
  plugins/desktop/frontend/src/components/Mention.tsx
  plugins/desktop/frontend/src/panes/CenterPane.tsx
    (ListeningGear, ThreadGear, listeningSummary,
     personaForActiveAgent, mention rendering, AutoReplyReason hint)
  plugins/desktop/frontend/src/panes/LeftPane.tsx
    (Agent DMs group lists AgentDM instances, Quick Create entry)
  plugins/desktop/frontend/src/App.tsx (Cmd/Ctrl+T, Cmd/Ctrl+N,
                                         Cmd/Ctrl+Shift+T)

## What handoff means here

The current branch `desktop-skeleton` carries everything above. No
smoke tests are committed (per project rule). Manual verification
order:

1. `go build / vet / test ./...` from repo root must be green.
2. `cd plugins/desktop/frontend && npx tsc --noEmit && npm run build`
   must be green.
3. Restart sumi server; open the desktop UI.
4. In any channel, run the seven verification flows above. Anything
   that diverges is a regression worth filing before the next phase.
5. The next phase picks up from `## Known boundaries`. None of those
   items block handoff.
