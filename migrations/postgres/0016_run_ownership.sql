ALTER TABLE agent_runs ADD COLUMN fencing_token TEXT;
ALTER TABLE agent_runs ADD COLUMN ownership_lease_expires_at TIMESTAMPTZ;
ALTER TABLE agent_runs ADD COLUMN last_renewed_at TIMESTAMPTZ;

UPDATE agent_runs
SET fencing_token = gen_random_uuid()::text,
    ownership_lease_expires_at = now() + interval '35 minutes',
    last_renewed_at = now()
WHERE status IN ('queued', 'running');

ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_active_ownership_check CHECK (
    status NOT IN ('queued', 'running')
    OR (
        fencing_token IS NOT NULL
        AND char_length(fencing_token) BETWEEN 1 AND 128
        AND ownership_lease_expires_at IS NOT NULL
        AND last_renewed_at IS NOT NULL
    )
);

CREATE INDEX agent_runs_expired_ownership_idx
    ON agent_runs (ownership_lease_expires_at)
    WHERE status IN ('queued', 'running');
