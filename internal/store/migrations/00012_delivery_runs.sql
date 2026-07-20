-- +goose Up
CREATE TABLE deliveries (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    inbox_item_id TEXT NOT NULL UNIQUE,
    trigger_message_id TEXT NOT NULL,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    trigger_target_sequence INTEGER NOT NULL CHECK (trigger_target_sequence >= 1),
    state TEXT NOT NULL CHECK (state IN ('available', 'accepted', 'completed')),
    created_at INTEGER NOT NULL,
    accepted_at INTEGER,
    completed_at INTEGER,
    UNIQUE (agent_id, trigger_message_id),
    FOREIGN KEY (inbox_item_id, agent_id) REFERENCES agent_inbox_items(id, agent_id) ON DELETE RESTRICT,
    FOREIGN KEY (trigger_message_id, space_id, target_kind, target_id, trigger_target_sequence)
        REFERENCES messages(id, space_id, target_kind, target_id, target_sequence) ON DELETE RESTRICT,
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    ),
    CHECK (
        (state = 'available' AND accepted_at IS NULL AND completed_at IS NULL)
        OR (state = 'accepted' AND accepted_at IS NOT NULL AND completed_at IS NULL)
        OR (state = 'completed' AND accepted_at IS NOT NULL AND completed_at IS NOT NULL)
    )
);

CREATE INDEX deliveries_agent_state_sequence
ON deliveries(agent_id, state, sequence);

CREATE UNIQUE INDEX deliveries_id_agent
ON deliveries(id, agent_id);

CREATE TABLE runs (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    delivery_id TEXT NOT NULL UNIQUE,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    basis_target_sequence INTEGER NOT NULL CHECK (basis_target_sequence >= 1),
    state TEXT NOT NULL CHECK (state IN ('accepted', 'running', 'completed')),
    outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'succeeded', 'failed')),
    result_kind TEXT NOT NULL DEFAULT '' CHECK (result_kind IN ('', 'message', 'held_draft')),
    result_id TEXT NOT NULL DEFAULT '',
    accepted_at INTEGER NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    FOREIGN KEY (delivery_id, agent_id) REFERENCES deliveries(id, agent_id) ON DELETE RESTRICT,
    CHECK (
        (state = 'accepted' AND outcome = '' AND result_kind = '' AND result_id = '' AND started_at IS NULL AND completed_at IS NULL)
        OR (state = 'running' AND outcome = '' AND result_kind = '' AND result_id = '' AND started_at IS NOT NULL AND completed_at IS NULL)
        OR (state = 'completed' AND outcome != '' AND result_kind != '' AND length(result_id) = 36 AND started_at IS NOT NULL AND completed_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX runs_id_agent
ON runs(id, agent_id);

CREATE UNIQUE INDEX runs_agent_one_active
ON runs(agent_id)
WHERE state IN ('accepted', 'running');

CREATE TABLE agent_run_fences (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE RESTRICT,
    current_fence INTEGER NOT NULL CHECK (current_fence >= 0)
);

CREATE TABLE run_launches (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    run_id TEXT NOT NULL,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    holder_computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
    holder_placement_generation INTEGER NOT NULL CHECK (holder_placement_generation > 0),
    fence INTEGER NOT NULL CHECK (fence > 0),
    claimed_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    closed_at INTEGER,
    close_reason TEXT NOT NULL DEFAULT '' CHECK (close_reason IN ('', 'replaced', 'completed')),
    FOREIGN KEY (run_id, agent_id) REFERENCES runs(id, agent_id) ON DELETE RESTRICT,
    UNIQUE (agent_id, fence),
    UNIQUE (id, run_id, fence),
    CHECK (expires_at > claimed_at),
    CHECK (
        (closed_at IS NULL AND close_reason = '')
        OR (closed_at IS NOT NULL AND close_reason = 'replaced' AND closed_at >= expires_at)
        OR (closed_at IS NOT NULL AND close_reason = 'completed' AND closed_at >= claimed_at)
    )
);

CREATE UNIQUE INDEX run_launches_run_current
ON run_launches(run_id)
WHERE closed_at IS NULL;

CREATE INDEX run_launches_agent_fence
ON run_launches(agent_id, fence);

CREATE TABLE run_completion_receipts (
    outbox_event_id TEXT PRIMARY KEY CHECK (length(outbox_event_id) = 36),
    request_id TEXT NOT NULL UNIQUE REFERENCES agent_requests(request_id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    run_id TEXT NOT NULL UNIQUE REFERENCES runs(id) ON DELETE RESTRICT,
    launch_id TEXT NOT NULL REFERENCES run_launches(id) ON DELETE RESTRICT,
    fence INTEGER NOT NULL CHECK (fence > 0),
    result_kind TEXT NOT NULL CHECK (result_kind IN ('message', 'held_draft')),
    result_id TEXT NOT NULL CHECK (length(result_id) = 36),
    committed_at INTEGER NOT NULL,
    FOREIGN KEY (launch_id, run_id, fence) REFERENCES run_launches(id, run_id, fence) ON DELETE RESTRICT
);

INSERT INTO deliveries(
    id, agent_id, inbox_item_id, trigger_message_id, space_id, target_kind,
    target_id, trigger_target_sequence, state, created_at
)
SELECT
    id,
    agent_id, id, trigger_message_id, space_id, target_kind,
    target_id, trigger_target_sequence, 'available', created_at
FROM agent_inbox_items
WHERE state IN ('unread', 'claimed');

-- +goose Down
DROP TABLE run_completion_receipts;
DROP INDEX run_launches_agent_fence;
DROP INDEX run_launches_run_current;
DROP TABLE run_launches;
DROP TABLE agent_run_fences;
DROP INDEX runs_agent_one_active;
DROP INDEX runs_id_agent;
DROP TABLE runs;
DROP INDEX deliveries_id_agent;
DROP INDEX deliveries_agent_state_sequence;
DROP TABLE deliveries;
