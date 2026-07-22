-- +goose Up
CREATE TABLE knowledge_generation_progress (
    generation INTEGER PRIMARY KEY REFERENCES knowledge_index_generations(generation) ON DELETE RESTRICT,
    snapshot_high_water INTEGER NOT NULL CHECK (snapshot_high_water >= 0),
    applied_sequence INTEGER NOT NULL CHECK (applied_sequence >= snapshot_high_water)
);

CREATE TABLE knowledge_projection_rows (
    generation INTEGER NOT NULL REFERENCES knowledge_index_generations(generation) ON DELETE RESTRICT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('message', 'work', 'artifact_version')),
    source_id TEXT NOT NULL CHECK (length(source_id) = 36),
    source_version INTEGER NOT NULL CHECK (source_version >= 0),
    revision BLOB NOT NULL CHECK (length(revision) = 32),
    fts_rowid INTEGER NOT NULL UNIQUE,
    PRIMARY KEY (generation, source_kind, source_id, source_version),
    CHECK (
        (source_kind IN ('message', 'work') AND source_version = 0)
        OR (source_kind = 'artifact_version' AND source_version >= 1)
    )
);

CREATE INDEX knowledge_projection_rows_generation_source
ON knowledge_projection_rows(generation, source_kind, source_id, source_version);

-- +goose Down
DROP INDEX knowledge_projection_rows_generation_source;
DROP TABLE knowledge_projection_rows;
DROP TABLE knowledge_generation_progress;
