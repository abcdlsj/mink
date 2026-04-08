# P2 RFC: Agent Team Chat

## Goal

P2 adds a true agent team mode to Mink:

- multiple agents share one visible conversation
- agents speak as distinct participants instead of hidden workers
- a leader orchestrates specialist turns and produces the final answer
- CLI and Telegram can show team collaboration as a first-class interaction model

This is intentionally different from the current multi-agent runtime. Today Mink has peer registration, routing, heartbeat, and delegation. P2 turns that execution substrate into a conversational team experience.

## What Problem This Solves

Current Mink multi-agent behavior is useful but not legible:

- `delegate` creates internal work, not visible group dialogue
- each delegated task behaves like background RPC plus polling
- CLI can show that agents are busy, but not why they are speaking or how they reach a conclusion
- users cannot put several agents into one shared thread and let them collaborate in public

For the product direction discussed in `#agent_research`, this is the missing piece. The goal is not only better orchestration. It is a visible `agent team` interaction model.

## Non-Goals

- free-form all-agent parallel chatter
- distributed consensus or autonomous social simulation
- cross-process or cross-machine coordination
- replacing the existing `delegate` flow for normal background work
- new channels beyond CLI and Telegram

P2 should start from a controlled turn-taking model. Unbounded group chat will generate noise, token waste, and race conditions.

## Product Principles

- One team conversation maps to one shared session.
- Every visible utterance belongs to one named agent.
- Turn-taking is explicit and serialized.
- The leader owns convergence and final answer quality.
- Specialists contribute only when asked or selected by policy.
- Team mode should be obvious in the UI, not hidden behind logs.

## Target UX

### CLI

The user starts a team conversation explicitly, for example with a command such as:

- `/team on`
- `/team off`
- `/team status`

When team mode is on:

- the transcript shows agent-authored lines such as `[Main]`, `[Researcher]`, `[Coder]`
- the sidebar shows current team members and current speaker
- delegation is no longer shown as opaque background work when it belongs to the team session
- the leader answer is visually recognizable as the final synthesis

### Telegram

The same thread can host several agents through one visible team transcript:

- the bot receives a user message for the team
- the leader decides who should speak next
- each team turn is emitted back into the thread with explicit agent identity
- the final answer is still owned by the leader unless the user addressed a specific specialist

## Core Design

### Shared Session

P2 uses one shared `session.Session` for the whole team conversation.

This is the key design choice:

- all team members read the same history
- each agent keeps its own system prompt and tool registry constraints
- conversation state is shared, but agent identity and behavior are not

This avoids inventing a second transcript model. The existing session abstraction is already the right place to store ordered conversation history.

### Message Attribution

Each session entry must identify which agent spoke.

Add to `msg.Message`:

- `AgentID string`

Rules:

- user messages keep empty `AgentID`
- visible team turns must set `AgentID`
- assistant turns outside team mode may keep current behavior or set the active agent ID for consistency

This is the minimum data needed for transcript rendering, replay, and future analytics.

### Team Identity

Add a `team_id` to bus payloads used for team orchestration.

This is not a global distributed identity. It is a runtime-scoped key used to:

- route turn messages to the right team session
- isolate concurrent team conversations
- allow one agent to participate in several teams without transcript collision

Suggested shape:

- `team:<session_id>`

## Turn Model

P2 should not start with unconstrained agent talk. Start with serialized turns.

### Required Invariant

One `team_id` has one active speaker at a time.

Without this invariant:

- two agents can read the same history snapshot
- both can generate replies concurrently
- both can append to the shared session out of order
- the resulting transcript becomes inconsistent and confusing

So team mode must add:

- a team-level turn lock
- a round counter
- a maximum round budget

### Initial Policies

Implement two turn policies:

1. `LeaderDriven`
2. `RoundRobin`

`LeaderDriven` should be the default and the only policy exposed in early UX.

#### LeaderDriven

Flow:

1. user sends a message to the team
2. leader receives the prompt first
3. leader can answer directly, or nominate one specialist at a time
4. specialist responds into the shared session
5. control returns to leader
6. leader either asks another specialist or emits the final answer

#### RoundRobin

Useful for demos and internal experiments only:

- fixed member order
- each member gets at most one turn per round
- leader closes

This policy should remain secondary because it is less efficient and less controllable.

## Speaker Selection

The cleanest starting point is explicit nomination by the leader.

Suggested tool:

- `mention(agent_name, question)`

Behavior:

- leader calls `mention("Researcher", "...")`
- runtime converts that to a team turn request
- selected agent receives the same shared history plus the local question
- selected agent emits one visible team message
- control returns to the leader

Why use a tool instead of implicit routing:

- it keeps the next speaker decision explicit
- it is easier to inspect in logs and runtime events
- it reduces accidental loops

This tool is only for team mode. It should not replace `delegate` for background execution.

## Runtime Components

### 1. Team State

Introduce a small coordinator type, for example:

- `agent/team.go`

Suggested fields:

- `ID`
- `SessionID`
- `LeaderAgentID`
- `Members []string`
- `Policy`
- `CurrentSpeaker`
- `Round`
- `MaxRounds`
- `Status` (`idle`, `running`, `waiting`, `done`, `failed`)

This state lives in memory first. It can become durable later if needed.

### 2. Team Dispatcher

Add a dispatcher layer responsible for:

- creating team state
- acquiring the per-team turn lock
- routing the next turn to the selected agent
- appending visible agent output into the shared session
- releasing control back to the policy

This should sit above the current `agent.Dispatcher`, not replace it.

That keeps the existing single-agent and delegate paths stable.

### 3. Bus Messages

Add team-specific bus message types:

- `team:start`
- `team:turn`
- `team:message`
- `team:done`

Payload should minimally include:

- `team_id`
- `session_id`
- `speaker_agent_id`
- `leader_agent_id`
- `round`
- `prompt`

The current bus already supports enough local coordination. P2 only needs clearer semantics, not a new transport.

## Session and Prompt Behavior

All team members use the same conversation history and different agent-local prompts.

Per turn:

1. build view from the shared session
2. inject the current speaker's system prompt and tools
3. append a local turn directive
4. run one serialized agent turn
5. append the resulting message with `AgentID`

The local turn directive should include:

- current team goal
- current round budget
- expected role of the current speaker
- whether the speaker should answer the user, contribute analysis, or nominate another speaker

## CLI Changes

CLI is where this feature becomes legible.

Required changes:

- transcript lines render speaker identity from `Message.AgentID`
- sidebar switches from generic `Agent Network` to team-specific state when a team session is active
- current speaker and round count are always visible
- team messages are visually separated from user messages without adding noisy borders

Recommended behavior:

- hide team sidebar when team mode is off
- show team member list only when more than one member participates
- emphasize the leader summary, not every intermediate thought

## Telegram Changes

Telegram should reuse the same orchestration model:

- one Telegram thread maps to one team session
- outgoing messages are prefixed by agent name
- the leader can keep the thread readable by summarizing after specialist turns

Example visible messages:

- `[Researcher] I found two likely causes...`
- `[Coder] The existing dispatcher can support this with a shared session...`
- `[Main] Conclusion: we should implement LeaderDriven first...`

## Failure Handling

### Team-Level Lock

Each `team_id` must serialize turns.

If a second turn request arrives while one is active:

- queue it or reject it with a clear runtime error

Do not allow parallel writes to the same team transcript.

### Loop Guard

Stop the team if:

- rounds exceed `MaxRounds`
- the same agent is nominated repeatedly beyond a threshold
- no agent can make progress

### Timeout

A specialist turn timeout should return control to the leader with an explicit failure event, not leave the team hanging.

### Fallback

If team orchestration fails, the leader should still be able to emit a final answer explaining that specialist collaboration failed.

## Implementation Plan

### Phase 1: Data and Rendering

- add `AgentID` to `msg.Message`
- render speaker labels in CLI transcript
- prefix Telegram output with agent name when applicable
- archive old RFCs and document the team model

### Phase 2: Runtime Team Core

- add team bus message types
- implement in-memory `TeamState`
- add team turn lock and round budget
- create `TeamDispatcher`

### Phase 3: Leader-Driven Team Mode

- add `/team on|off|status`
- add `mention(agent_name, question)` tool
- implement `LeaderDriven` policy
- make leader summary the default close behavior

### Phase 4: Polish

- add `RoundRobin` policy for demos
- improve sidebar and transcript presentation
- add runtime metrics for team turns and failure reasons

## Why This Is Better Than Reusing Delegate

`delegate` remains useful for hidden background execution.

It is the wrong abstraction for visible team collaboration because:

- it returns task-oriented status, not conversational turns
- it treats specialists as workers rather than participants
- it requires polling and private result flow
- it does not provide shared transcript semantics

P2 should keep both:

- `delegate` for background work
- `team` for visible collaboration

## Open Questions

1. Should team mode be opt-in per session only, or also configurable as the default for a given CLI profile?
2. Should specialist intermediate reasoning remain private even when their final answer is public?
3. Should team messages be stored as normal assistant messages with `AgentID`, or as a distinct entry kind?
4. When the user directly addresses one specialist, should control bypass the leader for that turn?

## Recommendation

Build P2 as a thin orchestration layer on top of the current multi-agent runtime.

Do not redesign the agent core again.

The smallest correct path is:

- shared session
- agent-attributed messages
- serialized turn policy
- visible transcript

That is enough to turn Mink from “multiple hidden workers” into a real `agent team` product surface.
