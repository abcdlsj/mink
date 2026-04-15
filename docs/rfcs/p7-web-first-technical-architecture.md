# P7 RFC: Web-First Technical Architecture

## Goal

Define the technical architecture for the next Mink generation without constraining the design to the current codebase.

This RFC assumes the product direction has already changed:

- Mink is a `workspace-native collaboration product`
- Web is the primary surface
- CLI and Telegram are secondary surfaces
- local agent execution must be decoupled from Web UI delivery

This RFC answers:

- what the core domain model is
- how Web, server, and local execution are separated
- how conversations map to executions
- how machines, drivers, memory, and artifacts are modeled
- what transport, storage, and event contracts are required

## Non-Goals

This RFC does not define:

- final UI/UX flows or visual style
- detailed component design
- a migration plan from the current Mink codebase
- every provider-specific driver behavior

This is a target architecture RFC, not a codebase archaeology document.

## Product Invariants

The technical design must preserve these product rules:

- `Workspace` is the top-level product scope.
- `Agent` is an identity, not a model alias.
- `Conversation` is the primary resumable collaboration object.
- `Thread` is a branch inside a conversation, not a second kind of top-level conversation.
- `Memory` is not transcript storage.
- `Execution` is not the main product object.
- Web UI must not directly own or embed local execution.

The key separation is:

- `conversation owns collaboration`
- `execution owns compute`

## Recommended Stack

This RFC assumes a split stack, not a single monolith.

### Web Product

- `TypeScript`
- `React`
- `Next.js` for the Web application shell, auth flows, and product pages
- `TanStack Query` for server state
- `Zustand` for local UI state
- `CSS Modules + CSS Variables` for design system implementation

This is the fastest path for a Web-first product team.

### Control Plane

- `TypeScript`
- `Hono` or equivalent lightweight server framework
- `Postgres` as canonical product data store
- `Redis` for presence, fan-out, and ephemeral coordination
- object storage for artifacts and large attachments

The control plane should be a distinct service even if it is deployed adjacent to the Web app.

### Machine Daemon

- `Go`
- single static binary
- local `SQLite` for daemon-side execution state, resume handles, and cache

Go is the right default for:

- local process supervision
- shell and PTY management
- cross-platform distribution
- low-overhead streaming
- driver lifecycle management

## High-Level Architecture

Mink should use three layers:

1. `Web UI / Product Surface`
2. `Control Plane / Server`
3. `Machine Daemon / Agent Service`

### 1. Web UI / Product Surface

Responsibilities:

- workspace navigation
- conversation surfaces
- channel and thread views
- agent DM and group collaboration
- memory and artifact presentation
- auth and permission-aware product interactions

The Web layer must not:

- own local shell execution
- speak directly to local drivers
- define provider-specific runtime semantics

### 2. Control Plane / Server

Responsibilities:

- identity and auth
- workspace, container, conversation, thread, and agent metadata
- memory metadata and artifact index
- machine registry and presence
- execution scheduling and state transitions
- event normalization and fan-out
- unread, mention, notification, and audit logic

The control plane is the canonical product truth.

### 3. Machine Daemon / Agent Service

Responsibilities:

- local execution capability
- runtime adapter hosting
- shell / repo / filesystem / credential access
- local execution persistence
- stream events back to the control plane
- collect and stage artifacts

The daemon should initially be one Go process.

It should contain:

- execution supervisor
- runtime adapters
- local session store
- artifact staging
- bridge server for external drivers if needed

If later needed, it can be split into a terminal host and host service.
That should not be the P1 default.

## Core Domain Model

### Workspace

Top-level working context.

Fields:

- `workspace_id`
- `name`
- `owner_id`
- `kind`
- `status`
- `metadata`

Owns:

- containers
- conversations
- threads
- agents
- memory scopes
- machine bindings
- artifacts

### Agent

Product identity used in DM or group collaboration.

Fields:

- `agent_id`
- `workspace_id`
- `name`
- `role`
- `description`
- `default_model`
- `default_driver`
- `tool_policy_id`
- `memory_scope_id`
- `presence`

Rules:

- an agent is not a model alias
- an agent may run on different machines over time
- an agent may participate in many containers and conversations

### Container

User-visible collaboration container.

Container types:

- `dm`
- `channel`

Fields:

- `container_id`
- `workspace_id`
- `type`
- `title`
- `topic`
- `status`

Rules:

- containers are navigation anchors
- DMs and channels are both containers
- threads are not containers

### Conversation

Primary resumable collaboration object.

Fields:

- `conversation_id`
- `workspace_id`
- `container_id`
- `title`
- `status`
- `active_model`
- `active_agent_set`
- `last_activity_at`

Rules:

- a container can hold many conversations over time
- a conversation is a stable product object
- a conversation can have many executions

### Thread

Scoped branch inside a conversation.

Fields:

- `thread_id`
- `workspace_id`
- `conversation_id`
- `parent_message_id`
- `title`
- `status`

Rules:

- thread is subordinate to conversation
- thread does not replace conversation as a top-level route family
- thread is used for focused sub-problems and local context isolation

### Memory

Structured, durable knowledge.

Memory scopes:

- `global`
- `workspace`
- `agent`
- `channel`
- `thread_scratchpad`

Rules:

- transcript is not memory
- thread scratchpad may be short-lived
- workspace and agent memory are durable and queryable

### Machine

Execution node registered by a local daemon.

Fields:

- `machine_id`
- `owner_id`
- `label`
- `platform`
- `capabilities`
- `presence`
- `last_seen_at`

Rules:

- a workspace may bind many machines
- a machine may serve many workspaces subject to policy
- machine identity is not exposed as a top-level collaboration object

### Execution

Concrete runtime instance for work.

Fields:

- `execution_id`
- `workspace_id`
- `conversation_id`
- `thread_id` optional
- `machine_id`
- `driver`
- `kind`
- `status`
- `started_at`
- `ended_at`
- `result_summary`

Kinds:

- `foreground_conversational`
- `background_task`

Rules:

- execution is not a first-class navigation root
- one conversation can own many executions
- execution may move across machines over time through retry or handoff

## State Model

The system must separate product state from compute state.

### Conversation State

Owned by the control plane.

Includes:

- messages
- participants
- container relation
- thread relation
- unread state
- memory links
- artifact index
- mentions
- presence summary

### Execution State

Owned jointly by control plane and machine daemon.

Includes:

- machine binding
- driver
- status
- stream cursor
- cancelability
- exit result
- runtime diagnostics

Rules:

- product UI defaults to conversation state
- execution state only appears in status bars, run details, and diagnostics views

## ID, URL, and Routing Model

The product URL model should only expose user-visible collaboration objects.

### Product IDs

- `workspace_id`
- `container_id`
- `conversation_id`
- `thread_id`
- `agent_id`

### Internal Execution IDs

- `execution_id`
- `machine_id`
- `driver_run_id`

These internal execution IDs must not become primary navigation keys.

### Web Routing

Recommended route shapes:

- `/w/:workspaceId`
- `/w/:workspaceId/c/:containerId/v/:conversationId`
- `/w/:workspaceId/c/:containerId/v/:conversationId/t/:threadId`
- `/w/:workspaceId/agents/:agentId`

Rules:

- `Workspace Home` is the default entry
- routes are centered on workspace, container, conversation, and thread
- execution and machine details stay behind panels or diagnostics routes

## Default Entry

The default product entry is:

- `Workspace Home`

It is not:

- an empty dashboard
- a separate Inbox product
- a raw transcript page

`Inbox` is a Workspace Home module or filter, not a separate product root.

Workspace Home should aggregate:

- recent conversations
- active channels
- open threads
- recent artifacts
- assigned agents
- attention-needed items

## Execution Architecture

### Control Channel

The daemon must connect outbound to the control plane.

The Web app must never connect directly to local machines.

Recommended transport:

- daemon to control plane: `WebSocket` bidirectional stream
- browser to control plane: `HTTP JSON + WebSocket`

Reasons:

- NAT-friendly
- simpler permission model
- multi-client fan-out
- consistent event transport

Database-backed command queues can exist as a fallback or recovery layer.
They should not be the primary realtime control mechanism.

### Runtime Adapters

The daemon must host a formal runtime adapter layer.

Required adapters:

- `CustomRuntimeAdapter`
- `ClaudeDriverAdapter`
- `CodexDriverAdapter`

Suggested interface:

```go
type RuntimeAdapter interface {
    Start(ctx context.Context, req ExecutionRequest) (ExecutionHandle, error)
    Resume(ctx context.Context, req ResumeRequest) (ExecutionHandle, error)
    Interrupt(ctx context.Context, executionID string) error
    Stream(ctx context.Context, executionID string) (<-chan RuntimeEvent, error)
    CollectArtifacts(ctx context.Context, executionID string) ([]ArtifactRef, error)
}
```

The control plane must not care which adapter generated the events.

### Conversation to Execution Mapping

Conversation and execution are intentionally decoupled.

One conversation may contain:

- many foreground executions across turns
- retries on another machine
- one foreground execution plus many background executions
- handoff from one driver to another

This is why `conversation` and `execution` must never collapse into one object.

### Local Daemon Persistence

The daemon must persist local execution state.

It should keep:

- active execution registry
- resume handles
- local transcript cache
- artifact staging index
- repo binding
- driver-specific session handles

This allows:

- Web reconnect
- control plane restart
- local resume
- driver restart recovery

## Workspace Binding Model

Machines are not globally trusted for every workspace by default.

Execution must run against an explicit workspace binding.

### Workspace Binding

Fields:

- `binding_id`
- `workspace_id`
- `machine_id`
- `repo_root`
- `env_profile`
- `allowed_tools`
- `memory_scopes`
- `default_driver_policy`

Rules:

- a machine can serve multiple workspaces
- each execution must use exactly one workspace binding
- bindings are the place where local repo and env context become valid

## Machine Registration and Auth

The machine trust model must be explicit in the RFC.

### Bootstrap Flow

1. user creates a machine enrollment token from the product
2. daemon starts with `server_url + enrollment_token`
3. daemon sends machine fingerprint and metadata
4. control plane issues:
   - `machine_id`
   - machine access token
   - refresh token or renewable session
5. machine begins presence and capability streaming

### Auth Rules

- bootstrap token is short-lived
- machine credential is machine-scoped
- workspace access comes from explicit bindings
- machine permissions are narrower than user permissions

### Offline Policy

If the target machine is offline:

- foreground execution request fails fast or prompts for reroute
- background execution can queue if policy allows
- conversation remains readable and resumable
- execution state shows degraded availability, not broken conversation state

## Unified Event Schema

All product and runtime streaming must normalize into one event envelope.

### Event Envelope

```json
{
  "event_id": "evt_123",
  "type": "message.delta",
  "workspace_id": "w_123",
  "container_id": "c_123",
  "conversation_id": "v_123",
  "thread_id": "t_123",
  "execution_id": "x_123",
  "actor": {
    "kind": "agent",
    "id": "a_123"
  },
  "seq": 42,
  "timestamp": "2026-04-15T16:00:00Z",
  "payload": {}
}
```

### Required Event Types

- `message.created`
- `message.delta`
- `message.completed`
- `tool.call.started`
- `tool.call.completed`
- `artifact.created`
- `memory.linked`
- `execution.status.changed`
- `execution.failed`
- `thread.updated`
- `presence.updated`

### Event Rules

- provider-private output must be normalized before fan-out
- UI must consume product events, not provider-specific wire formats
- replay and audit should be possible from normalized event history

## Memory Model

Memory must be separate from transcript and treated as a first-class system.

### Canonical Memory Layers

- `global memory`
- `workspace memory`
- `agent memory`
- `channel memory`
- `thread scratchpad`

### Server-Side Canonical Memory

Stored in the control plane and indexed for search, permissioning, and retrieval.

Good for:

- durable facts
- team conventions
- agent-specific preferences
- workspace docs and operating rules

### Machine-Side Local Context

Local-only execution context that does not have to be uploaded.

Examples:

- repo indexes
- temporary files
- shell environment
- local caches

This must not be confused with canonical memory.

## Memory Write Path

The RFC must define who can write memory and where.

### Allowed Write Rules

- transcript is always written automatically
- thread scratchpad may be written automatically by execution
- agent, channel, and workspace memory should default to proposal-first
- global memory should be user-controlled only

### P1 Policy

- user can `pin` or `save as memory`
- agent can `propose memory`
- channel memory requires owner or authorized member confirmation
- workspace memory requires explicit confirmation unless policy allows automatic write
- global memory never auto-writes in P1

This prevents memory from collapsing back into transcript summarization.

## Artifacts

Artifacts are first-class execution outputs.

Examples:

- code snippets
- patch previews
- files
- logs
- screenshots
- structured reports

Artifacts should be:

- indexed at conversation scope
- linkable from messages and memory
- optionally staged locally first, then uploaded

## Borrowed from Slock

The architecture should directly borrow these ideas:

- `local daemon + cloud control plane + web ui`
- machine as a first-class object
- local capability ownership at the daemon
- server-mediated coordination instead of browser-to-local direct calls

These are strong and reusable architectural decisions.

## Deliberately Different in Mink

Mink must deliberately differ from slock in these ways:

- not task-dispatch-first
- not command-queue-first
- not single-run-first
- not “spawn new process for every task and forget continuity”

Mink needs:

- conversation continuity
- many executions per conversation
- driver switching
- resume and handoff
- thread-aware collaboration
- memory-aware workspace execution

Slock is a useful reference for daemon and machine separation.
It is not the target product model.

## Implementation Phases

### Phase 1: Product Foundation

- define canonical domain tables
- ship workspace, container, conversation, thread, agent, machine, execution records
- ship normalized event schema

### Phase 2: Daemon Foundation

- implement machine registration
- implement single Go daemon
- implement workspace bindings
- implement execution supervisor

### Phase 3: Driver Layer

- ship custom runtime adapter
- ship Claude driver adapter
- ship Codex driver adapter
- ship artifact staging and collection

### Phase 4: Web Product

- ship Workspace Home
- ship conversation surfaces
- ship thread context pane
- ship run details and diagnostics

### Phase 5: Secondary Surfaces

- map CLI to the new domain model as a power-user surface
- map Telegram or Discord to lightweight conversation surfaces

## Success Criteria

This RFC is successful when:

- Web can treat conversations as stable collaboration objects
- execution can move independently of conversation identity
- custom, Claude, and Codex runtimes can be attached through one daemon contract
- local machine execution is safely isolated from the Web app
- memory and transcript no longer collapse into one layer
- future CLI and Telegram surfaces can reuse the same domain model without redefining it
