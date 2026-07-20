package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type AgentPlacement struct {
	AgentID    string
	ComputerID string
	Generation uint64
	State      string
	ErrorCode  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type AcknowledgePlacementParams struct {
	ComputerID      string
	RegistrationKey string
	AgentID         string
	Generation      uint64
	State           string
	ErrorCode       string
	Now             time.Time
}

func (s *Store) SetAgentPlacement(ctx context.Context, agentID, computerID string, now time.Time) (AgentPlacement, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("begin set placement: %w", err)
	}
	defer tx.Rollback()

	if exists, err := recordExists(ctx, tx, "agents", agentID); err != nil {
		return AgentPlacement{}, err
	} else if !exists {
		return AgentPlacement{}, ErrAgentNotFound
	}
	if exists, err := recordExists(ctx, tx, "computers", computerID); err != nil {
		return AgentPlacement{}, err
	} else if !exists {
		return AgentPlacement{}, ErrComputerNotFound
	}

	current, err := placementByAgent(ctx, tx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		stamp := unixNano(now)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_placements(agent_id, computer_id, generation, state, error_code, created_at, updated_at)
			VALUES(?, ?, 1, 'pending', '', ?, ?)
		`, agentID, computerID, stamp, stamp); err != nil {
			return AgentPlacement{}, fmt.Errorf("persist placement: %w", err)
		}
		current, err = placementByAgent(ctx, tx, agentID)
	} else if err == nil && current.ComputerID == computerID && (current.State == "pending" || current.State == "active") {
		if err := tx.Commit(); err != nil {
			return AgentPlacement{}, fmt.Errorf("commit idempotent placement: %w", err)
		}
		return current, nil
	} else if err == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_placements
			SET computer_id = ?, generation = generation + 1, state = 'pending', error_code = '', updated_at = max(updated_at, ?)
			WHERE agent_id = ?
		`, computerID, unixNano(now), agentID); err != nil {
			return AgentPlacement{}, fmt.Errorf("replace placement: %w", err)
		}
		current, err = placementByAgent(ctx, tx, agentID)
	}
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("read placement after set: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AgentPlacement{}, fmt.Errorf("commit set placement: %w", err)
	}
	return current, nil
}

func (s *Store) GetAgentPlacement(ctx context.Context, agentID string) (AgentPlacement, error) {
	placement, err := scanPlacement(s.db.QueryRowContext(ctx, placementSelect+" WHERE agent_id = ?", agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentPlacement{}, ErrPlacementNotFound
	}
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("get placement: %w", err)
	}
	return placement, nil
}

func (s *Store) ListAgentPlacements(ctx context.Context) ([]AgentPlacement, error) {
	rows, err := s.db.QueryContext(ctx, placementSelect+" ORDER BY agent_id")
	if err != nil {
		return nil, fmt.Errorf("list placements: %w", err)
	}
	defer rows.Close()
	return scanPlacements(rows)
}

func (s *Store) ListComputerAssignments(ctx context.Context, computerID, registrationKey string) ([]AgentPlacement, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list assignments: %w", err)
	}
	defer tx.Rollback()
	if err := authenticateComputer(ctx, tx, computerID, registrationKey); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, placementSelect+" WHERE computer_id = ? AND state = 'pending' ORDER BY agent_id", computerID)
	if err != nil {
		return nil, fmt.Errorf("list computer assignments: %w", err)
	}
	assignments, err := scanPlacements(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit list assignments: %w", err)
	}
	return assignments, nil
}

func (s *Store) AcknowledgeAgentPlacement(ctx context.Context, params AcknowledgePlacementParams) (AgentPlacement, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("begin placement acknowledgement: %w", err)
	}
	defer tx.Rollback()
	if err := authenticateComputer(ctx, tx, params.ComputerID, params.RegistrationKey); err != nil {
		return AgentPlacement{}, err
	}
	current, err := placementByAgent(ctx, tx, params.AgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentPlacement{}, ErrPlacementNotFound
	}
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("read placement for acknowledgement: %w", err)
	}
	if current.ComputerID != params.ComputerID || current.Generation != params.Generation {
		return AgentPlacement{}, ErrPlacementStale
	}
	if current.State == params.State && current.ErrorCode == params.ErrorCode {
		if err := tx.Commit(); err != nil {
			return AgentPlacement{}, fmt.Errorf("commit idempotent acknowledgement: %w", err)
		}
		return current, nil
	}
	if current.State != "pending" {
		return AgentPlacement{}, ErrPlacementConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_placements
		SET state = ?, error_code = ?, updated_at = max(updated_at, ?)
		WHERE agent_id = ? AND computer_id = ? AND generation = ? AND state = 'pending'
	`, params.State, params.ErrorCode, unixNano(params.Now), params.AgentID, params.ComputerID, params.Generation); err != nil {
		return AgentPlacement{}, fmt.Errorf("persist placement acknowledgement: %w", err)
	}
	current, err = placementByAgent(ctx, tx, params.AgentID)
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("read acknowledged placement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AgentPlacement{}, fmt.Errorf("commit placement acknowledgement: %w", err)
	}
	return current, nil
}

const placementSelect = `
	SELECT agent_id, computer_id, generation, state, error_code, created_at, updated_at
	FROM agent_placements`

func placementByAgent(ctx context.Context, tx *sql.Tx, agentID string) (AgentPlacement, error) {
	return scanPlacement(tx.QueryRowContext(ctx, placementSelect+" WHERE agent_id = ?", agentID))
}

func scanPlacement(row scanner) (AgentPlacement, error) {
	var placement AgentPlacement
	var createdAt, updatedAt int64
	if err := row.Scan(&placement.AgentID, &placement.ComputerID, &placement.Generation, &placement.State, &placement.ErrorCode, &createdAt, &updatedAt); err != nil {
		return AgentPlacement{}, err
	}
	placement.CreatedAt = timeFromUnixNano(createdAt)
	placement.UpdatedAt = timeFromUnixNano(updatedAt)
	return placement, nil
}

func scanPlacements(rows *sql.Rows) ([]AgentPlacement, error) {
	var placements []AgentPlacement
	for rows.Next() {
		placement, err := scanPlacement(rows)
		if err != nil {
			return nil, fmt.Errorf("scan placement: %w", err)
		}
		placements = append(placements, placement)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate placements: %w", err)
	}
	return placements, nil
}

func authenticateComputer(ctx context.Context, tx *sql.Tx, computerID, registrationKey string) error {
	keyHash := sha256.Sum256([]byte(registrationKey))
	var matches bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM computers WHERE id = ? AND registration_key_hash = ?)
	`, computerID, keyHash[:]).Scan(&matches); err != nil {
		return fmt.Errorf("authenticate computer: %w", err)
	}
	if matches {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM computers WHERE id = ?)", computerID).Scan(&exists); err != nil {
		return fmt.Errorf("check computer for authentication: %w", err)
	}
	if !exists {
		return ErrComputerNotFound
	}
	return ErrRegistrationKeyMismatch
}

func recordExists(ctx context.Context, tx *sql.Tx, table, id string) (bool, error) {
	query := "SELECT EXISTS(SELECT 1 FROM " + table + " WHERE id = ?)"
	var exists bool
	if err := tx.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check %s identity: %w", table, err)
	}
	return exists, nil
}
