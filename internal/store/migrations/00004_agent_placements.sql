-- +goose Up
CREATE TABLE agent_placements (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE RESTRICT,
    computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'active', 'failed')),
    error_code TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (
        (state = 'failed' AND length(error_code) BETWEEN 1 AND 64)
        OR (state IN ('pending', 'active') AND error_code = '')
    )
);

CREATE INDEX agent_placements_computer_state_agent
ON agent_placements(computer_id, state, agent_id);

-- +goose Down
DROP INDEX agent_placements_computer_state_agent;
DROP TABLE agent_placements;
