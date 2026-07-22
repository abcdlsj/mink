-- +goose Up
CREATE TABLE knowledge_index_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    active_generation INTEGER NOT NULL DEFAULT 0 CHECK (active_generation >= 0),
    next_generation INTEGER NOT NULL DEFAULT 0 CHECK (next_generation >= 0),
    status TEXT NOT NULL CHECK (status IN ('ready', 'rebuilding', 'degraded')),
    CHECK (next_generation = 0 OR next_generation > active_generation)
);

INSERT INTO knowledge_index_metadata(singleton, status) VALUES(1, 'degraded');

CREATE TABLE knowledge_index_generations (
    generation INTEGER PRIMARY KEY CHECK (generation > 0),
    state TEXT NOT NULL CHECK (state IN ('building', 'complete', 'corrupt')),
    created_at INTEGER NOT NULL
);

CREATE TABLE knowledge_dirty_sources (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('message', 'work', 'artifact_version')),
    source_id TEXT NOT NULL CHECK (length(source_id) = 36),
    source_version INTEGER NOT NULL DEFAULT 0 CHECK (source_version >= 0),
    revision BLOB NOT NULL CHECK (length(revision) = 32),
    enqueued_at INTEGER NOT NULL,
    CHECK (
        (source_kind IN ('message', 'work') AND source_version = 0)
        OR (source_kind = 'artifact_version' AND source_version >= 1)
    )
);

CREATE INDEX knowledge_dirty_sources_sequence ON knowledge_dirty_sources(sequence);

CREATE VIRTUAL TABLE knowledge_fts USING fts5(
    source_kind UNINDEXED,
    source_id UNINDEXED,
    source_version UNINDEXED,
    generation UNINDEXED,
    revision UNINDEXED,
    body
);

-- +goose Down
DROP TABLE knowledge_fts;
DROP INDEX knowledge_dirty_sources_sequence;
DROP TABLE knowledge_dirty_sources;
DROP TABLE knowledge_index_generations;
DROP TABLE knowledge_index_metadata;
