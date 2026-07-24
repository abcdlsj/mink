package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/abcdlsj/sumi/internal/tool"
)

func (s *State) Load(ctx context.Context, run tool.RunContext, callID string) (tool.StoredResult, bool, error) {
	var result tool.StoredResult
	var requestHash, resultJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT request_hash, result_json FROM tool_results
		WHERE agent_id = ? AND computer_id = ? AND placement_desired_revision = ?
		  AND run_id = ? AND attempt = ? AND fence = ? AND call_id = ?
	`, run.AgentID, run.ComputerID, run.PlacementDesiredRevision, run.RunID, run.Attempt, run.Fence, callID).Scan(&requestHash, &resultJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return tool.StoredResult{}, false, nil
	}
	if err != nil {
		return tool.StoredResult{}, false, fmt.Errorf("read tool result: %w", err)
	}
	if len(requestHash) != len(result.RequestHash) {
		return tool.StoredResult{}, false, errors.New("stored tool request hash is invalid")
	}
	copy(result.RequestHash[:], requestHash)
	result.Result = append(result.Result, resultJSON...)
	return result, true, nil
}

func (s *State) Save(ctx context.Context, run tool.RunContext, callID string, result tool.StoredResult) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_results(
			agent_id, computer_id, placement_desired_revision, run_id, attempt, fence,
			call_id, request_hash, result_json, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.AgentID, run.ComputerID, run.PlacementDesiredRevision, run.RunID, run.Attempt, run.Fence,
		callID, result.RequestHash[:], []byte(result.Result), time.Now().UTC().UnixNano())
	if err == nil {
		return nil
	}
	stored, found, loadErr := s.Load(ctx, run, callID)
	if loadErr != nil {
		return loadErr
	}
	if found && stored.RequestHash == result.RequestHash && bytes.Equal(stored.Result, result.Result) {
		return nil
	}
	return fmt.Errorf("persist tool result: %w", err)
}

var _ tool.ResultStore = (*State)(nil)
