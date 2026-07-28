ALTER TABLE local_agent_runs ADD COLUMN fencing_token TEXT NOT NULL DEFAULT '';
ALTER TABLE local_agent_runs ADD COLUMN ownership_lease_expires_at TEXT;
