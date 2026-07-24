package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *State) StartRun(ctx context.Context, journal RunJournal) error {
	if !validID(journal.AgentID) || !validID(journal.RunID) || journal.PlacementDesiredRevision == 0 ||
		journal.Attempt == 0 || journal.Fence == 0 || journal.State != "running" || journal.StartedAt.IsZero() || journal.FinishedAt != nil {
		return errors.New("run journal is invalid")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO run_journals(agent_id, placement_desired_revision, run_id, attempt, fence, state, started_at)
		VALUES(?, ?, ?, ?, ?, 'running', ?)
		ON CONFLICT(agent_id, run_id, attempt, fence) DO NOTHING
	`, journal.AgentID, journal.PlacementDesiredRevision, journal.RunID, journal.Attempt, journal.Fence, unixNano(journal.StartedAt))
	if err != nil {
		return fmt.Errorf("persist run journal: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) FinishRun(ctx context.Context, agentID, runID string, attempt, fence uint64, state string, finishedAt time.Time) error {
	if !validID(agentID) || !validID(runID) || attempt == 0 || fence == 0 ||
		(state != "completed" && state != "cancelled" && state != "failed") || finishedAt.IsZero() {
		return errors.New("run journal completion is invalid")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE run_journals SET state = ?, finished_at = ?
		WHERE agent_id = ? AND run_id = ? AND attempt = ? AND fence = ? AND state = 'running'
	`, state, unixNano(finishedAt), agentID, runID, attempt, fence)
	if err != nil {
		return fmt.Errorf("finish run journal: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("running journal not found")
	}
	return s.secureSQLiteFiles()
}

func (s *State) RunJournals(ctx context.Context, agentID string) ([]RunJournal, error) {
	if !validID(agentID) {
		return nil, errors.New("journal agent id is invalid")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, placement_desired_revision, run_id, attempt, fence, state, started_at, finished_at
		FROM run_journals WHERE agent_id = ? ORDER BY started_at, run_id
	`, agentID)
	if err != nil {
		return nil, fmt.Errorf("list run journals: %w", err)
	}
	defer rows.Close()
	var journals []RunJournal
	for rows.Next() {
		var journal RunJournal
		var startedAt int64
		var finishedAt sql.NullInt64
		if err := rows.Scan(&journal.AgentID, &journal.PlacementDesiredRevision, &journal.RunID, &journal.Attempt,
			&journal.Fence, &journal.State, &startedAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("scan run journal: %w", err)
		}
		journal.StartedAt = fromUnixNano(startedAt)
		if finishedAt.Valid {
			value := fromUnixNano(finishedAt.Int64)
			journal.FinishedAt = &value
		}
		journals = append(journals, journal)
	}
	return journals, rows.Err()
}
