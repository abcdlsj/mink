ALTER TABLE computer_commands ADD COLUMN result_event_id TEXT
    CHECK (char_length(result_event_id) BETWEEN 1 AND 128);

CREATE UNIQUE INDEX computer_commands_result_event_idx
    ON computer_commands (result_event_id) WHERE result_event_id IS NOT NULL;
