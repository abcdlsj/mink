package sqlite

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'local',
  status TEXT NOT NULL DEFAULT 'active',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workspaces_updated ON workspaces(updated_at);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  snapshot_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_workspace_updated ON sessions(workspace_id, updated_at);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT 'ws_default',
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

CREATE INDEX IF NOT EXISTS idx_tasks_status_updated ON tasks(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_updated ON tasks(workspace_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_source ON tasks(workspace_id, source_kind, source_id, thread_id);
CREATE INDEX IF NOT EXISTS idx_tasks_source ON tasks(source_kind, source_id, thread_id);

CREATE TABLE IF NOT EXISTS runs (
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

CREATE INDEX IF NOT EXISTS idx_runs_task_started ON runs(task_id, started_at);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status, started_at);

CREATE TABLE IF NOT EXISTS events (
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_events_run_seq ON events(run_id, seq);
CREATE INDEX IF NOT EXISTS idx_events_task_created ON events(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_events_type_created ON events(type, created_at);

CREATE TABLE IF NOT EXISTS artifacts (
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

CREATE INDEX IF NOT EXISTS idx_artifacts_task_kind ON artifacts(task_id, kind);

CREATE TABLE IF NOT EXISTS memory_docs (
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

CREATE VIRTUAL TABLE IF NOT EXISTS memory_docs_fts USING fts5(
  title,
  summary,
  body,
  content='memory_docs',
  content_rowid='rowid'
);

CREATE TABLE IF NOT EXISTS source_bindings (
  workspace_id TEXT NOT NULL DEFAULT 'ws_default',
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  thread_id TEXT NOT NULL DEFAULT '',
  active_task_id TEXT NOT NULL,
  active_session_id TEXT NOT NULL DEFAULT '',
  team_id TEXT NOT NULL DEFAULT '',
  team_thread_id TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, source_kind, source_id, thread_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_source_bindings_workspace_key
ON source_bindings(workspace_id, source_kind, source_id, thread_id);

CREATE TABLE IF NOT EXISTS teams (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT 'ws_default',
  name TEXT NOT NULL,
  leader_agent_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  turn_policy TEXT NOT NULL DEFAULT 'leader_driven',
  max_rounds INTEGER NOT NULL DEFAULT 6,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_teams_status ON teams(status);
CREATE INDEX IF NOT EXISTS idx_teams_workspace_status ON teams(workspace_id, status, updated_at);

CREATE TABLE IF NOT EXISTS team_members (
  team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL,
  role_name TEXT NOT NULL DEFAULT '',
  role_description TEXT NOT NULL DEFAULT '',
  member_type TEXT NOT NULL DEFAULT 'persistent',
  profile_json TEXT NOT NULL DEFAULT '{}',
  joined_at TEXT NOT NULL,
  PRIMARY KEY (team_id, agent_id)
);

CREATE TABLE IF NOT EXISTS team_threads (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT 'ws_default',
  team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  session_id TEXT NOT NULL DEFAULT '',
  current_round INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_team_threads_team ON team_threads(team_id, status);
CREATE INDEX IF NOT EXISTS idx_team_threads_workspace_team ON team_threads(workspace_id, team_id, status, updated_at);

CREATE TABLE IF NOT EXISTS agent_identities (
  workspace_id TEXT NOT NULL DEFAULT 'ws_default',
  agent_id TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  profile TEXT NOT NULL DEFAULT '',
  memory_scope TEXT NOT NULL DEFAULT '',
  tool_constraints_json TEXT NOT NULL DEFAULT '[]',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_identities_workspace_created
ON agent_identities(workspace_id, created_at);
`
