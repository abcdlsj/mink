ALTER TABLE local_agent_runs ADD COLUMN stop_state TEXT NOT NULL DEFAULT 'none'
    CHECK (stop_state IN ('none', 'stopping', 'orphaned', 'reaped'));
ALTER TABLE local_agent_runs ADD COLUMN stop_epoch INTEGER NOT NULL DEFAULT 0
    CHECK (stop_epoch >= 0);
ALTER TABLE local_agent_runs ADD COLUMN stop_requested_at TEXT;
ALTER TABLE local_agent_runs ADD COLUMN sigterm_sent_at TEXT;
ALTER TABLE local_agent_runs ADD COLUMN sigkill_sent_at TEXT;
ALTER TABLE local_agent_runs ADD COLUMN orphaned_at TEXT;
ALTER TABLE local_agent_runs ADD COLUMN reaped_at TEXT;
ALTER TABLE local_agent_runs ADD COLUMN exit_code INTEGER;
ALTER TABLE local_agent_runs ADD COLUMN exit_signal INTEGER;
ALTER TABLE local_agent_runs ADD COLUMN stop_error_code TEXT;
