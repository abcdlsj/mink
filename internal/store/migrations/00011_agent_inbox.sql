-- +goose Up
CREATE TABLE message_mentions (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 63),
    PRIMARY KEY (message_id, agent_id),
    UNIQUE (message_id, ordinal)
);

CREATE INDEX message_mentions_agent_message
ON message_mentions(agent_id, message_id);

CREATE UNIQUE INDEX messages_inbox_trigger
ON messages(id, space_id, target_kind, target_id, target_sequence);

CREATE UNIQUE INDEX threads_id_space
ON threads(id, space_id);

CREATE TABLE agent_space_mutes (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    muted_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, space_id)
);

CREATE TABLE agent_thread_follows (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    thread_root_message_id TEXT NOT NULL,
    followed_at INTEGER NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('mention', 'reply', 'explicit')),
    PRIMARY KEY (agent_id, thread_root_message_id),
    FOREIGN KEY (thread_root_message_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT
);

CREATE INDEX agent_thread_follows_space
ON agent_thread_follows(agent_id, space_id, thread_root_message_id);

CREATE TABLE agent_target_cursors (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    seen_up_to_target_sequence INTEGER NOT NULL CHECK (seen_up_to_target_sequence >= 0),
    observed_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, target_kind, target_id),
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    )
);

CREATE TABLE agent_inbox_items (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    trigger_message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE RESTRICT,
    trigger_target_sequence INTEGER NOT NULL CHECK (trigger_target_sequence >= 1),
    reason TEXT NOT NULL CHECK (reason IN ('dm', 'mention', 'thread_follow')),
    state TEXT NOT NULL CHECK (state IN ('unread', 'claimed', 'done')),
    claimed_at INTEGER,
    done_at INTEGER,
    completion TEXT NOT NULL DEFAULT '' CHECK (completion IN ('', 'sent', 'cancelled', 'silent', 'access_lost')),
    created_at INTEGER NOT NULL,
    UNIQUE (agent_id, trigger_message_id),
    FOREIGN KEY (trigger_message_id, space_id, target_kind, target_id, trigger_target_sequence)
        REFERENCES messages(id, space_id, target_kind, target_id, target_sequence) ON DELETE RESTRICT,
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    ),
    CHECK (
        (state = 'unread' AND claimed_at IS NULL AND done_at IS NULL AND completion = '')
        OR (state = 'claimed' AND claimed_at IS NOT NULL AND done_at IS NULL AND completion = '')
        OR (state = 'done' AND done_at IS NOT NULL AND completion != '')
    )
);

CREATE INDEX agent_inbox_items_agent_state_sequence
ON agent_inbox_items(agent_id, state, sequence);

CREATE UNIQUE INDEX agent_inbox_items_id_agent
ON agent_inbox_items(id, agent_id);

CREATE TABLE agent_held_drafts (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    inbox_item_id TEXT NOT NULL,
    predecessor_draft_id TEXT REFERENCES agent_held_drafts(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL,
    basis_target_sequence INTEGER NOT NULL CHECK (basis_target_sequence >= 0),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 400000),
    held_reason TEXT NOT NULL CHECK (held_reason = 'target_advanced'),
    state TEXT NOT NULL CHECK (state IN ('held', 'sent', 'cancelled', 'superseded', 'retargeted')),
    resolution_action TEXT NOT NULL DEFAULT '' CHECK (resolution_action IN ('', 'retry', 'cancel', 'retarget')),
    result_kind TEXT NOT NULL DEFAULT '' CHECK (result_kind IN ('', 'message', 'held_draft')),
    result_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (inbox_item_id, agent_id) REFERENCES agent_inbox_items(id, agent_id) ON DELETE RESTRICT,
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR (target_kind = 'thread' AND length(target_id) = 36)
    ),
    CHECK (
        (state = 'held' AND resolution_action = '' AND result_kind = '' AND result_id = '')
        OR (state = 'cancelled' AND resolution_action = 'cancel' AND result_kind = '' AND result_id = '')
        OR (state = 'sent' AND resolution_action = 'retry' AND result_kind = 'message' AND length(result_id) = 36)
        OR (state = 'superseded' AND resolution_action = 'retry' AND result_kind = 'held_draft' AND length(result_id) = 36)
        OR (state = 'retargeted' AND resolution_action = 'retarget' AND result_kind IN ('message', 'held_draft') AND length(result_id) = 36)
    )
);

CREATE UNIQUE INDEX agent_held_drafts_predecessor
ON agent_held_drafts(predecessor_draft_id)
WHERE predecessor_draft_id IS NOT NULL;

CREATE INDEX agent_held_drafts_agent_state_sequence
ON agent_held_drafts(agent_id, state, sequence);

CREATE TABLE agent_held_draft_mentions (
    draft_id TEXT NOT NULL REFERENCES agent_held_drafts(id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 63),
    PRIMARY KEY (draft_id, agent_id),
    UNIQUE (draft_id, ordinal)
);

CREATE TABLE agent_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    operation TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 64),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    response_snapshot BLOB NOT NULL CHECK (length(response_snapshot) > 0),
    committed_at INTEGER NOT NULL
);

CREATE INDEX agent_requests_agent_committed
ON agent_requests(agent_id, committed_at);

-- +goose Down
DROP INDEX agent_requests_agent_committed;
DROP TABLE agent_requests;
DROP TABLE agent_held_draft_mentions;
DROP INDEX agent_held_drafts_agent_state_sequence;
DROP INDEX agent_held_drafts_predecessor;
DROP TABLE agent_held_drafts;
DROP INDEX agent_inbox_items_id_agent;
DROP INDEX agent_inbox_items_agent_state_sequence;
DROP TABLE agent_inbox_items;
DROP TABLE agent_target_cursors;
DROP INDEX agent_thread_follows_space;
DROP TABLE agent_thread_follows;
DROP TABLE agent_space_mutes;
DROP INDEX message_mentions_agent_message;
DROP TABLE message_mentions;
DROP INDEX threads_id_space;
DROP INDEX messages_inbox_trigger;
