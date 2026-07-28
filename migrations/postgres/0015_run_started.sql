ALTER TABLE agent_runs ADD COLUMN run_attempt INTEGER
    CHECK (run_attempt IS NULL OR run_attempt > 0);
ALTER TABLE agent_runs ADD COLUMN process_instance_id UUID;
ALTER TABLE agent_runs ADD COLUMN started_event_id TEXT
    CHECK (char_length(started_event_id) BETWEEN 1 AND 128);

CREATE UNIQUE INDEX agent_runs_started_event_idx
    ON agent_runs (started_event_id) WHERE started_event_id IS NOT NULL;
