# P0 RFC: Durable Execution, Memory, and Task Flow

## Goal

P0 turns Mink from a session-centric local agent into a durable execution runtime with:

- first-class `Task`, `Run`, `Event`, and `Artifact`
- append-only execution history
- long-term memory backed by Markdown and SQLite FTS5
- resumable task flow across process restarts and session boundaries

Out of scope for P0:

- peer multi-agent routing
- new channels beyond CLI and Telegram
- MCP and external tool protocol compatibility
- remote skill marketplace

## Problems in the Current Design

- `source` is the runtime key in `agent.Dispatcher`, so agent lifecycle is bound to CLI/Telegram entrypoints instead of durable work units.
- `session.FileStore` writes full JSON snapshots, which is enough for conversation recovery but not for replay, audit, recovery, or durable orchestration.
- `bus.Bus` is an in-process coordination layer, not a durable execution ledger.
- `spawn` and background work have transient completion paths rather than a shared run/task model.
- memory does not exist as agent-readable and agent-writable long-term state.

## Design Principles

- `Task` is the user-visible unit of work.
- `Run` is one execution attempt for a task.
- `Event` is the append-only source of truth.
- `Session` becomes a view derived from task and run history, not the primary runtime record.
- Markdown is the canonical human-readable memory format.
- SQLite is the canonical machine-readable index and durable execution store.
- `source` remains useful for ingress and reply routing, but it is not the primary identity of work.

## Core Data Model

### Task

Represents durable work owned by the runtime.

Suggested fields:

- `id`
- `kind` (`conversation`, `cron`, `delegated`, `system`)
- `title`
- `status` (`todo`, `queued`, `running`, `waiting`, `review`, `done`, `failed`, `cancelled`)
- `priority`
- `source_kind` (`cli`, `telegram`, `system`)
- `source_id`
- `thread_id`
- `created_at`
- `updated_at`
- `closed_at`
- `parent_task_id`
- `current_run_id`
- `memory_scope`
- `metadata_json`

### Run

Represents one execution attempt of a task.

Suggested fields:

- `id`
- `task_id`
- `agent_id`
- `trigger` (`user_input`, `cron`, `resume`, `delegate`, `system`)
- `status` (`queued`, `running`, `waiting_input`, `completed`, `failed`, `interrupted`, `cancelled`)
- `started_at`
- `finished_at`
- `resume_from_event_id`
- `last_event_seq`
- `session_id`
- `summary`
- `error_message`
- `metadata_json`

### Event

Append-only immutable record. This is the replay and audit source.

Suggested fields:

- `id`
- `task_id`
- `run_id`
- `seq`
- `type`
- `actor_type` (`user`, `agent`, `tool`, `system`)
- `actor_id`
- `source_kind`
- `source_id`
- `thread_id`
- `payload_json`
- `created_at`

Event types for P0:

- `task.created`
- `task.queued`
- `task.started`
- `task.status_changed`
- `task.completed`
- `task.failed`
- `task.cancelled`
- `run.started`
- `run.resumed`
- `run.completed`
- `run.failed`
- `input.received`
- `assistant.emitted`
- `tool.called`
- `tool.completed`
- `tool.failed`
- `memory.note_added`
- `memory.doc_indexed`
- `session.bound`
- `session.compacted`

### Artifact

Represents durable outputs produced by runs or tools.

Suggested fields:

- `id`
- `task_id`
- `run_id`
- `kind` (`message`, `file`, `memory_doc`, `log`, `summary`)
- `uri`
- `mime_type`
- `sha256`
- `metadata_json`
- `created_at`

### Memory Document

Markdown file on disk plus SQL index entry.

Frontmatter example:

```yaml
---
id: mem_20260407_001
title: Mink runtime direction
kind: design-note
tags: [runtime, durable-execution, memory]
task_id: task_123
run_id: run_456
source: agent_research
updated_at: 2026-04-07T12:00:00+08:00
summary: Durable execution must precede peer coordination.
---
```

## Storage Layout

Recommended directory layout under `.mink/`:

```text
.mink/
  runtime/
    runtime.db
    runtime.db-shm
    runtime.db-wal
  memory/
    inbox/
    working/
    knowledge/
    summaries/
  sessions/
    legacy/
```

Rules:

- SQLite WAL mode enabled.
- Markdown files stored in stable paths and indexed into SQLite.
- old JSON sessions kept for migration and fallback.

## SQLite Schema

```sql
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 0,
  source_kind TEXT,
  source_id TEXT,
  thread_id TEXT,
  parent_task_id TEXT,
  current_run_id TEXT,
  memory_scope TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  closed_at TEXT
);

CREATE INDEX idx_tasks_status_updated ON tasks(status, updated_at);
CREATE INDEX idx_tasks_source ON tasks(source_kind, source_id, thread_id);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL,
  trigger TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  resume_from_event_id TEXT,
  last_event_seq INTEGER NOT NULL DEFAULT 0,
  session_id TEXT,
  summary TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_runs_task_started ON runs(task_id, started_at);
CREATE INDEX idx_runs_status ON runs(status, started_at);

CREATE TABLE events (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id TEXT REFERENCES runs(id) ON DELETE SET NULL,
  seq INTEGER NOT NULL,
  type TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  source_kind TEXT,
  source_id TEXT,
  thread_id TEXT,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_events_run_seq ON events(run_id, seq);
CREATE INDEX idx_events_task_created ON events(task_id, created_at);
CREATE INDEX idx_events_type_created ON events(type, created_at);

CREATE TABLE artifacts (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id TEXT REFERENCES runs(id) ON DELETE SET NULL,
  kind TEXT NOT NULL,
  uri TEXT NOT NULL,
  mime_type TEXT NOT NULL DEFAULT '',
  sha256 TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX idx_artifacts_task_kind ON artifacts(task_id, kind);

CREATE TABLE memory_docs (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  kind TEXT NOT NULL,
  tags_json TEXT NOT NULL DEFAULT '[]',
  task_id TEXT,
  run_id TEXT,
  source TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  indexed_at TEXT NOT NULL
);

CREATE VIRTUAL TABLE memory_docs_fts USING fts5(
  title,
  summary,
  body,
  content='memory_docs',
  content_rowid='rowid'
);

CREATE TABLE source_bindings (
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  thread_id TEXT NOT NULL DEFAULT '',
  active_task_id TEXT NOT NULL,
  active_session_id TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (source_kind, source_id, thread_id)
);
```

## Runtime Services

P0 should introduce these internal services:

### TaskStore

- CRUD for `tasks`
- status transitions
- active task lookup by source binding

### RunStore

- create and finish runs
- resume lookup
- run-level summaries and errors

### EventStore

- append-only writes
- replay by task or run
- resume cursor lookup

### MemoryStore

- file-backed Markdown persistence
- SQLite index writes
- FTS query
- recency and tag filters

### SourceBindingStore

- map `cli/chat/thread` to current active task
- replace current `session.Manager.sources` as the durable binding layer

## Package Refactor

Suggested new packages:

- `runtime/task`
- `runtime/run`
- `runtime/event`
- `runtime/artifact`
- `runtime/store/sqlite`
- `memory`
- `memory/index`
- `memory/docs`

Existing package changes:

### `session`

- keep `Session` as a rendered conversation view
- stop treating session snapshot as primary truth
- add adapter to rebuild session state from events
- keep existing JSON store only for migration and rollback

### `agent.Dispatcher`

- resolve or create `Task` from source binding before execution
- create a `Run` per execution attempt
- append `input.received`, `run.started`, `assistant.emitted`, `run.completed` events
- no longer keep business truth in `agents[src]`; source becomes routing state

### `agent.Agent`

- emit tool and assistant events through `EventStore`
- checkpoint progress via `last_event_seq`
- load memory context via `MemoryStore`
- bind current session ID to run metadata rather than using session as the root object

### `tool/background` and `tool/spawn`

- produce child tasks and runs, not ad-hoc transient callbacks
- background completion should append `task.completed` or `task.failed`

### `cron.Scheduler`

- create tasks with `kind=cron`
- enqueue them into the same task flow instead of directly creating a temporary session

### `bus`

- stays in-process for P0
- do not make it the source of truth
- use it for dispatch and notifications only, with SQLite-backed stores as durable truth

## Task Flow

P0 task flow state machine:

- `todo`
- `queued`
- `running`
- `waiting`
- `review`
- `done`
- `failed`
- `cancelled`

Transitions for P0:

- new inbound request: `todo -> queued -> running`
- assistant asks for user input: `running -> waiting`
- successful completion: `running -> review` or `running -> done`
- retryable failure: `running -> failed -> queued`
- manual cancellation: `queued|running|waiting -> cancelled`

Rules:

- only one active run per task in P0
- every transition must emit an event
- recovery logic should derive current status from latest persisted state, not in-memory worker state

## Recovery Model

On process start:

1. load tasks in `queued`, `running`, and retry-eligible `failed`
2. mark stale `running` runs as `interrupted`
3. create resumed runs with `trigger=resume`
4. rebuild source bindings and active sessions
5. continue execution from the last durable event

On inbound message:

1. resolve source binding
2. attach to existing waiting task if present
3. otherwise create a new task and run
4. persist inbound event before starting execution

## Memory Model

P0 memory scopes:

- `inbox`: unprocessed notes and externally supplied facts
- `working`: task-local active memory
- `knowledge`: durable cross-task memory
- `summaries`: compacted rollups

Core operations:

- `remember(note, scope, tags)`
- `search(query, scope, recency, tags)`
- `summarize(task_id|run_id)`
- `compact(scope)`

Indexing flow:

1. write Markdown file
2. parse frontmatter with `github.com/adrg/frontmatter`
3. hash body
4. upsert into `memory_docs`
5. refresh FTS row
6. append `memory.doc_indexed` event

Recommended implementation:

- SQLite driver: `zombiezen.com/go/sqlite`
- file watcher: `github.com/fsnotify/fsnotify`

## Migration Plan

Phase-in migration without breaking current users:

1. keep current `session` JSON snapshots
2. introduce runtime SQLite alongside existing sessions
3. create source bindings from current session mappings
4. start emitting events for all new runs
5. add a compatibility path that renders sessions from both legacy snapshots and new events
6. once stable, stop writing full snapshots on every turn and keep snapshots only for compaction/export

## Two-Week Implementation Plan

### Week 1

Day 1:

- add `runtime/store/sqlite`
- initialize SQLite schema and WAL mode
- add ID generation helpers

Day 2:

- implement `TaskStore`, `RunStore`, `EventStore`
- add append and replay APIs

Day 3:

- introduce `SourceBindingStore`
- refactor dispatcher ingress from `source -> session` to `source -> task/run`

Day 4:

- make `agent.Agent` emit run and message events
- persist tool call and tool result events

Day 5:

- make `cron` create tasks and runs
- make `background` completion write task/run events

### Week 2

Day 6:

- add `memory` package
- write Markdown documents with frontmatter
- add SQLite indexing for memory docs

Day 7:

- implement FTS query and scope filters
- inject memory retrieval into agent prompt assembly

Day 8:

- add task recovery on process restart
- stale run interruption and resume logic

Day 9:

- derive `Session` view from task/run/event history
- add compatibility adapter for old session snapshots

Day 10:

- integration testing on CLI and Telegram
- recovery test matrix
- documentation and migration notes

## Acceptance Criteria

- restart during an active task does not lose the task
- inbound Telegram/CLI messages resume the correct waiting task
- cron and background work appear in the same durable task ledger
- every run has replayable events
- memory notes can be written as Markdown and queried via FTS
- current CLI and Telegram UX still works without requiring new channels

## Main Risks

- partial migration creates split-brain between old sessions and new runtime state
- over-coupling session rendering with event storage slows down rollout
- FTS synchronization bugs can make Markdown and SQL index diverge
- recovery semantics become unclear if multiple workers can pick the same task

## Risk Controls

- keep session compatibility layer during P0
- enforce a single active run per task
- write all state transitions and tool completions through stores before side-effectful replies
- add repair job to rebuild memory index from Markdown files
