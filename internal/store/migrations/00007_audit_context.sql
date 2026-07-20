-- +goose Up
ALTER TABLE audit_events
ADD COLUMN context_kind TEXT NOT NULL DEFAULT ''
CHECK (context_kind IN ('', 'space', 'thread'));

ALTER TABLE audit_events
ADD COLUMN context_id TEXT NOT NULL DEFAULT ''
CHECK (
    (context_kind = '' AND context_id = '')
    OR (context_kind IN ('space', 'thread') AND length(context_id) = 36)
);

-- +goose Down
ALTER TABLE audit_events DROP COLUMN context_id;
ALTER TABLE audit_events DROP COLUMN context_kind;
