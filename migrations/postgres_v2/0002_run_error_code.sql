ALTER TABLE agent_runs ADD COLUMN error_code TEXT;

ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_error_code_values
    CHECK (error_code IN ('invalid_command', 'agent_unavailable', 'process_lost', 'session_lost', 'sandbox_unavailable', 'driver_unavailable', 'internal'));

ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_error_code_requires_failure
    CHECK (error_code IS NULL OR outcome_code = 'failed');
