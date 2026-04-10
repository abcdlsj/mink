# P5 RFC: Persistence Unification

## Goal

Refactor Mink persistence around two clear rules:

1. `Workspace` is a first-class durable key.
2. `SQLite` is the source of truth for runtime and session state, while `Markdown` remains the canonical human-readable memory format.

This RFC is intentionally broader than a bug fix.
It is a storage-model cleanup for:

- session recovery
- Web / CLI / Telegram source binding
- team and thread continuity
- replay and audit
- agent-readable memory retrieval
- future multi-workspace product surfaces

## Why This RFC Exists

Today Mink persistence is split across three different shapes:

1. session snapshots in `~/.mink/sessions/*.json`
2. runlog traces in `~/.mink/sessions/*.log.jsonl`
3. runtime state in `~/.mink/runtime/runtime.db`

This is workable for local recovery, but not coherent enough for the product direction we are now building.

Recent Web UI bugs exposed the real problem:

- session lists are not properly workspace-scoped
- `source` is overloaded as both an ingress identity and a storage isolation key
- transcript, execution activity, and runtime state are stored in different places and read back through different codepaths
- memory is partly durable, but its relationship to session / run / workspace is still too loose

The core issue is not “which file format is nicer”.
The core issue is that Mink still lacks one explicit persistence model.

## Product Judgment

Mink should persist three different kinds of knowledge, but each should have a single primary home:

1. `Runtime and session state`
   - primary home: `SQLite`
2. `Human-readable long-term memory`
   - primary home: `Markdown files`
3. `Derived caches / exports / replay artifacts`
   - optional, rebuildable, not source of truth

This means:

- `SQLite` should own sessions, runs, events, bindings, teams, threads, and indexes.
- `Markdown` should remain the memory authoring and reading surface because agents and humans both benefit from direct file-based reading.
- `JSON session snapshots` and `JSONL runlogs` should be removed from the primary runtime path.

## Non-Goals

This RFC does not require:

- moving memory itself into SQLite blobs
- building cross-machine replication

This RFC does allow a destructive cutover.
Old session and runlog data do not need to be preserved.

## Design Principles

- `Workspace` is a durable entity, not only a filesystem convention.
- `Source` remains an ingress / reply identity, not the primary identity of durable work.
- `Binding` is the relationship between a workspace-scoped source and an active session/task.
- `Event` is the append-only runtime truth.
- `Session` is a durable conversation view built from persisted entries and events.
- `Markdown memory` must stay easy for agents to search, read, and write directly.
- `SQLite` should index memory and own the links between memory and runtime objects.
- Every major object should be queryable by `workspace_id`.

## Current Problems

### 1. Workspace is implicit

Today workspace mostly appears as:

- current process cwd
- session directory selection
- UI label text

That is not enough.
If workspace is implicit, we cannot robustly support:

- Web and CLI sharing the right session list
- one process viewing multiple workspaces later
- workspace-aware search and resume

### 2. Source is overloaded

Today `source` is used for all of the following:

- ingress routing
- current session lookup
- active task binding
- team/thread binding
- some recovery semantics

This was acceptable for a local single-surface tool, but it is too overloaded for Mink’s current direction.

`source` should answer:

- where did this input come from?
- where should replies go?

It should not also have to answer:

- which workspace owns this state?
- which durable session family does this input belong to?

### 3. Sessions are split from runtime history

Current session JSON snapshots are good for rebuilding a transcript quickly.
Current JSONL runlogs are good for debugging.
Current SQLite runtime events are good for durable orchestration.

The problem is that no one layer is fully authoritative for all session-like questions.

This creates drift:

- transcript says one thing
- runlog says another thing
- runtime.db knows something else

### 4. Memory is durable but not fully integrated

The current memory design has one strong property:

- Markdown files are easy for both humans and agents to read

That is the right direction.
But its integration is incomplete:

- memory docs are indexed in SQLite, but workspace ownership is not first-class
- memory scope and runtime ownership are not yet cleanly unified
- agent retrieval semantics still depend on multiple ad hoc codepaths

## Core Decision

### Persistence Model

Mink should converge on this model:

- `SQLite` stores:
  - workspaces
  - source bindings
  - sessions
  - session entries
  - tasks
  - runs
  - events
  - artifacts
  - teams
  - threads
  - memory indexes
- `Markdown` stores:
  - agent-readable and human-readable memory documents
- `JSON / JSONL` become:
  - optional debug export only

## Data Model

### Workspace

New first-class entity:

- `id`
- `path`
- `name`
- `kind` (`local`, later maybe `remote`)
- `status`
- `metadata_json`
- `created_at`
- `updated_at`

Rules:

- `path` is not the primary key
- `id` is stable
- path changes are allowed later

### Source

`Source` remains a transport / ingress identity.

Examples:

- `platform:cli`
- `platform:web`
- `telegram:12345`

Source should remain semantically stable.
Long-term we should not encode workspace into the source string as the canonical design.

### Source Binding

New authoritative binding model:

- `(workspace_id, source_kind, source_id, thread_id)` -> active state

Suggested fields:

- `workspace_id`
- `source_kind`
- `source_id`
- `thread_id`
- `active_task_id`
- `active_run_id`
- `active_session_id`
- `active_team_id`
- `active_thread_id`
- `updated_at`

This is the proper place for workspace isolation.

### Session

Session becomes a durable workspace-owned object.

Suggested fields:

- `id`
- `workspace_id`
- `kind` (`main`, `team_thread`, `dm`, `channel`, `agent_conversation`)
- `title`
- `status`
- `parent_session_id`
- `fork_from_entry_seq`
- `summary`
- `latest_anchor_id`
- `created_at`
- `updated_at`
- `closed_at`
- `metadata_json`

This replaces the current model where a session is mostly “a JSON file with entries”.

### Session Entry

Instead of storing the durable conversation only as JSON snapshot entries, promote entries into SQLite.

Suggested fields:

- `id`
- `session_id`
- `seq`
- `entry_kind` (`user`, `assistant`, `tool`, `system`, `note`)
- `agent_id`
- `message_json`
- `created_at`

Why:

- session transcript becomes queryable
- no need to rebuild every UI from ad hoc file parsing
- transcript, replay, and search can share one base

### Task / Run / Event

Keep the existing direction from P0 durable execution, but make them explicitly workspace-owned.

Add `workspace_id` to:

- `tasks`
- `runs`
- `events`
- `artifacts`

Rationale:

- every durable unit of work belongs to a workspace
- recovery and filtering become cheap and explicit

### Team / Thread

Also make these explicitly workspace-owned.

Add `workspace_id` to:

- `teams`
- `team_threads`
- `agent_identities` when needed through scope tables or metadata

This avoids later confusion where a team looks global but is actually local to a repo/workspace.

## Memory Model

### Canonical Form

Memory remains Markdown on disk.

That decision should stay.
It is good because:

- agents can read raw Markdown directly
- humans can inspect and edit memory without special tooling
- git workflows are possible when desired
- docs and notes remain portable

### Required Improvement

Memory should become workspace-aware and easier for agents to query intentionally.

Each memory document should carry:

- `memory_id`
- `workspace_id`
- `scope`
- `owner_kind` (`workspace`, `team`, `agent`, `task`, `run`)
- `owner_id`
- `title`
- `summary`
- `tags`
- `source_refs`
- `updated_at`

Markdown frontmatter example:

```yaml
---
id: mem_01
workspace_id: ws_mink_local
scope: knowledge
owner_kind: workspace
owner_id: ws_mink_local
title: Dispatcher Turn Rules
tags: [dispatcher, team, runtime]
source_refs:
  - run:run_123
  - task:task_456
updated_at: 2026-04-10T11:00:00+08:00
summary: Dispatcher serializes visible turns and emits status lines for long-running work.
---
```

### Memory Index in SQLite

SQLite should index Markdown memory docs as it already does, but the table should be upgraded to include:

- `workspace_id`
- `scope`
- `owner_kind`
- `owner_id`
- `source_uri`
- `path`
- `title`
- `summary`
- `body`
- `tags_json`
- `updated_at`
- `indexed_at`

### Agent Retrieval

Agent retrieval should work like this:

1. agent knows current `workspace_id`
2. agent knows current task/run/team/thread context
3. agent issues targeted memory queries:
   - by workspace
   - by owner
   - by tags
   - by full-text search
4. agent reads the Markdown body directly when needed

This is the most important product property for memory:

- memory is not only stashed
- memory is actively readable

## Source of Truth Rules

After P5, the intended truth layers are:

1. `SQLite events`
   - authoritative runtime history
2. `SQLite sessions + session_entries`
   - authoritative conversation view state
3. `Markdown memory docs`
   - authoritative memory content
4. `SQLite memory index`
   - authoritative memory search/index layer, rebuildable from Markdown
5. `JSON/JSONL`
   - optional export only, not part of the runtime persistence contract

## Storage Layout

Recommended layout under `.mink/`:

```text
.mink/
  runtime/
    runtime.db
    runtime.db-wal
    runtime.db-shm
  memory/
    workspaces/
      <workspace-id>/
        inbox/
        working/
        knowledge/
        summaries/
  exports/
    sessions/
    runlogs/
```

Important difference from today:

- session durability no longer depends on `.json` files in `sessions/`
- JSON/JSONL are no longer read by the runtime

## SQLite Schema Direction

This RFC does not require a final DDL dump yet, but the target additions are:

- `workspaces`
- `sessions`
- `session_entries`
- `workspace_id` on tasks/runs/events/artifacts/source_bindings/teams/team_threads
- richer `memory_docs`

Suggested high-level schema:

```sql
CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'local',
  status TEXT NOT NULL DEFAULT 'active',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id),
  kind TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  parent_session_id TEXT,
  fork_from_entry_seq INTEGER NOT NULL DEFAULT 0,
  summary TEXT NOT NULL DEFAULT '',
  latest_anchor_id TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  closed_at TEXT
);

CREATE TABLE session_entries (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  entry_kind TEXT NOT NULL,
  agent_id TEXT NOT NULL DEFAULT '',
  message_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_session_entries_seq ON session_entries(session_id, seq);
```

## Interaction With Current Source System

Long-term target:

- keep stable source keys such as `platform:web`
- do not permanently encode workspace into source strings
- move isolation into `source_bindings.workspace_id`

Cutover rule:

- after P5 lands, new runtime behavior reads only the new workspace-aware binding model
- old source encodings and old session files may be ignored or deleted

## Migration Plan

### Phase 1: Introduce First-Class Workspace

- add `workspaces` table
- assign a stable workspace record at startup
- add `workspace_id` to runtime rows

### Phase 2: Move Sessions Into SQLite

- add `sessions` and `session_entries`
- make SQLite the only runtime session store
- stop reading legacy JSON snapshots

### Phase 3: Upgrade Memory Metadata

- add `workspace_id`, `owner_kind`, `owner_id`, `scope`
- resync Markdown docs into upgraded SQLite index
- make agent memory retrieval workspace-aware

### Phase 4: Simplify Source Binding

- bind `(workspace_id, source)` to active session/task state
- stop relying on workspace-encoded source strings as the durable model

## Risks

### 1. Broken cutover

If the cutover is incomplete, some surfaces may still read removed legacy files or old binding shapes.

Mitigation:

- delete legacy read paths decisively
- keep one truth path only

### 2. Memory index drift

Markdown and SQLite index can diverge.

Mitigation:

- keep `Sync` / watcher repair path
- make reindex rebuild cheap and explicit

### 3. Workspace identity instability

If `workspace_id` is derived only from the current path and the repo moves, identity may drift.

Mitigation:

- store stable workspace row on first open
- allow path updates later

### 4. Overloading runtime.db too early

If every concern is forced into SQLite without clear boundaries, we may create a giant unstructured blob store.

Mitigation:

- keep Markdown as the canonical memory document layer
- keep message payloads structured
- use projection tables deliberately

## Open Questions

1. Should `workspace_id` be generated from path on first open, or persisted in a small workspace marker file under `.mink/`?
2. Should team memory be implemented as a special memory owner type, or as a separate first-class table plus Markdown projection?
3. Should session titles remain derived from first user input, or become explicit mutable metadata?
4. Should replay UI read from `events` directly, or from a prebuilt session activity projection?

## Recommendation

Start P5 with the smallest real unification step:

1. add first-class `workspace`
2. add `workspace_id` to runtime entities
3. add `sessions` and `session_entries` to SQLite
4. keep Markdown memory and strengthen its metadata/indexing
5. remove JSON/JSONL from runtime read paths entirely

This is the cleanest path to the product we are already implicitly building:

- workspace-aware
- session-resumable
- agent-readable memory
- durable multi-surface runtime

## Success Criteria

P5 is successful when:

- session listing is workspace-correct
- Web / CLI / Telegram bind through the same workspace-aware model
- one authoritative path exists for session recovery
- memory retrieval is intentionally queryable by agents
- replay and process visibility no longer require reading ad hoc debug files
- new product surfaces can treat `Workspace` as a real object, not a side effect of cwd
