# P2 RFC: Agent Team Chat

## Goal

P2 adds a true agent team mode to Mink:

- multiple agents share one visible conversation
- agents speak as distinct participants instead of hidden workers
- a leader orchestrates specialist turns and produces the final answer
- CLI becomes the primary surface for visible team collaboration
- Telegram stays leader-facing and does not try to simulate a full multi-agent group chat

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
- turning Telegram into a full visible team transcript
- hard-coded specialist rosters such as permanent `Researcher`, `Coder`, `Reviewer` personas

P2 should start from a controlled turn-taking model. Unbounded group chat will generate noise, token waste, and race conditions.

## Product Principles

- One team conversation maps to one shared session.
- Every visible utterance belongs to one named agent.
- Turn-taking is explicit and serialized.
- The leader owns convergence and final answer quality.
- Specialists contribute only when asked or selected by policy.
- Team roles are synthesized per task, not pre-generated as a fixed roster.
- Team mode should be obvious in the UI, not hidden behind logs.
- Execution profiles may be stable; team roles should remain dynamic.
- Agents are durable identities with their own memory and can participate in multiple teams over time.
- Teams must be resumable across process restarts, not treated as disposable chat artifacts.

## Target UX

### CLI

The user starts a team conversation explicitly, for example with a command such as:

- `/team on`
- `/team off`
- `/team status`

When team mode is on:

- the transcript shows agent-authored lines such as `[Main]`, `[Researcher]`, `[Coder]`
- the UI switches into a dedicated team console instead of trying to extend the normal single-agent layout
- the team rail shows current team members, current speaker, round, and leader goal
- delegation is no longer shown as opaque background work when it belongs to the team session
- the leader answer is visually recognizable as the final synthesis
- specialist turns are visually lighter than the leader closeout
- the current speaker has an explicit thinking state so the user does not stare at a blank console

### Telegram

Telegram should not try to act like a full team chat surface.

Recommended behavior:

- the bot receives a user message for the team
- internal specialist collaboration may happen behind the scenes
- Telegram receives the leader answer only
- Telegram may optionally receive a short collaboration summary such as `Consulted: architecture, implementation`

This keeps Telegram aligned with its single-bot identity and avoids a stream of fake multi-agent chatter.

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

### Dynamic Role Synthesis

P2 should not rely on a pre-generated list of team roles.

Instead, when the user starts a team interaction:

1. the leader analyzes the task
2. the leader decides which specialist roles are needed
3. the runtime creates temporary role definitions for this team only
4. those roles are bound to execution profiles that can actually run them

This is the core distinction:

- `role`: task-specific responsibility, generated at runtime
- `profile`: stable execution capability, defined by the runtime

Examples:

- role: `DSL Config Investigator`
- role: `Frontend Rendering Challenger`
- role: `Release Risk Reviewer`

These roles should not have to exist in config before the task begins.

Dynamic roles can be fulfilled in two ways:

- bound to an existing persistent agent identity
- fulfilled by a temporary specialist created for the current team

P2 must support both. Temporary specialists are useful, but they are not the whole model.

### Execution Profiles

The runtime still needs stable execution profiles underneath team mode.

Profiles define things such as:

- model choice
- tool allowlist
- max concurrency
- capability family

P2 should keep these stable profiles in config, while allowing the leader to synthesize temporary team roles that map onto them.

### Agent Persistence and Team Resume

P2 should assume that `agent` and `team` are both durable runtime concepts.

#### Persistent Agents

Persistent agents have:

- stable identity
- durable memory
- stable profile and tool constraints
- ability to participate in different teams over time

Examples:

- a long-lived `Main` agent
- a persistent `Research` agent with its own memory base
- a persistent `Release` agent that can be pulled into different collaborations

#### Temporary Specialists

Temporary specialists still exist, but they are a fallback, not the only model.

They are appropriate when:

- no persistent agent matches the synthesized role
- the leader needs a one-off temporary perspective
- the role is too task-specific to justify a long-lived identity

#### Resumable Teams

Teams must survive process restarts and runtime interruptions.

That means P2 should treat the following as durable state:

- `team_id`
- shared `session_id`
- leader identity
- current members and their roles
- current turn policy
- round count
- current speaker
- team status

This state should be recoverable from the durable runtime store introduced in P0.

#### Membership Model

Role synthesis chooses work to be done. It does not force all team members to be ephemeral.

Suggested member kinds:

- `persistent`: existing durable agent joins the team
- `ephemeral`: runtime creates a temporary specialist for this team only

This lets the leader say:

- pull in an existing persistent agent when identity and memory matter
- spawn a temporary specialist when only a one-off role is needed

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

- `spawn_specialist(role_name, role_description, profile_hint)`
- `invite_agent(agent_id, role_name, task)`
- `mention(agent_name, question)`

Behavior:

- leader calls `spawn_specialist("Config Investigator", "...", "analysis")`
- runtime materializes a temporary team member bound to a matching execution profile
- or leader calls `invite_agent("agent:research", "Config Investigator", "...")`
- runtime attaches an existing persistent agent to the team with the synthesized role
- leader calls `mention("Config Investigator", "...")`
- runtime converts that to a team turn request
- selected agent receives the same shared history plus the local question
- selected agent emits one visible team message
- control returns to the leader

Why use a tool instead of implicit routing:

- it keeps the next speaker decision explicit
- it is easier to inspect in logs and runtime events
- it reduces accidental loops

These tools are only for team mode. They should not replace `delegate` for background execution.

## Runtime Components

### 1. Team State

Introduce a small coordinator type, for example:

- `agent/team.go`

Suggested fields:

- `ID`
- `SessionID`
- `LeaderAgentID`
- `Members []string`
- `Roles []TeamRole`
- `MemberKinds map[string]string`
- `Policy`
- `CurrentSpeaker`
- `Round`
- `MaxRounds`
- `Status` (`idle`, `running`, `waiting`, `done`, `failed`)

This state should be durably recoverable, not only kept in memory.

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
- `role_name`
- `member_kind`
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

## Team Console UX

CLI is where this feature becomes legible. This needs a dedicated team layout, not a few extra labels on the current single-agent console.

Required changes:

- transcript lines render speaker identity from `Message.AgentID`
- transcript lines use consistent identity styling per speaker
- a dedicated team rail replaces the generic sidebar when a team session is active
- current speaker and round count are always visible
- leader summary is visually distinct from specialist turns
- specialist turns can use lighter styling, indentation, or toned-down color
- team console should avoid heavy borders and preserve the lighter CLI direction already established

Recommended behavior:

- hide team rail when team mode is off
- show only information that helps understand collaboration
- do not show static empty agent slots before roles are synthesized
- emphasize the leader summary, not every intermediate thought
- show which speaker is currently thinking so long gaps feel intentional

## Telegram Changes

Telegram should use a different rendering strategy from CLI:

- one Telegram thread maps to one team session
- specialist collaboration remains internal by default
- outgoing messages are leader-owned
- optional specialist contribution summaries are condensed into one short appendix

Example visible output:

- main answer from the leader
- optional suffix such as `Consulted: config analysis, rendering review`

This is not just a UI choice. It preserves the fact that Telegram users are talking to one bot identity.

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
- add optional `TeamID` to message or bus payloads where needed for routing and rendering
- render speaker labels in CLI transcript
- document Telegram as leader-only output
- archive old RFCs and document the team model

### Phase 2: Runtime Team Core

- add team bus message types
- implement durable `TeamState`
- add `TeamRole` and dynamic role synthesis flow
- support both `persistent` and `ephemeral` members
- add team turn lock and round budget
- create `TeamDispatcher`
- add team recovery on runtime resume

### Phase 3: Leader-Driven Team Mode

- add `/team on|off|status`
- add `spawn_specialist(role_name, role_description, profile_hint)` tool
- add `invite_agent(agent_id, role_name, task)` tool
- add `mention(agent_name, question)` tool
- implement `LeaderDriven` policy
- make leader summary the default close behavior

### Phase 4: Polish

- add `RoundRobin` policy for demos
- ship dedicated CLI team console styling
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
4. When the user directly addresses one specialist, should control bypass the leader for that turn? Initial recommendation: no.
5. Should `MaxRounds` stay fixed at 6 by default, or become configurable per team template or session?
6. When both a persistent agent and a temporary specialist fit the same synthesized role, what should the leader prefer by default?

## Recommendation

Build P2 as a thin orchestration layer on top of the current multi-agent runtime.

Do not redesign the agent core again.

The smallest correct path is:

- shared session
- agent-attributed messages
- dynamic role synthesis on top of stable execution profiles
- persistent agents plus optional ephemeral specialists
- serialized turn policy
- resumable team state on top of the P0 durable runtime
- CLI-first visible transcript
- leader-only Telegram surface

That is enough to turn Mink from “multiple hidden workers” into a real `agent team` product surface.
