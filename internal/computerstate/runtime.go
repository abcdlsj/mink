package computerstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *State) SaveRuntimeSession(ctx context.Context, session RuntimeSession) error {
	if !validID(session.AgentID) || !validID(session.ComputerID) || session.PlacementGeneration == 0 || session.Token == "" || session.ExpiresAt.IsZero() || session.UpdatedAt.IsZero() {
		return errors.New("runtime session is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save runtime session: %w", err)
	}
	defer tx.Rollback()
	existing, found, err := readRuntimeSession(tx.QueryRowContext(ctx, `
		SELECT agent_id, computer_id, placement_generation, token, expires_at, updated_at
		FROM runtime_sessions WHERE agent_id = ?
	`, session.AgentID))
	if err != nil {
		return err
	}
	if found {
		if session.PlacementGeneration < existing.PlacementGeneration {
			return errors.New("runtime session placement generation is stale")
		}
		if session.PlacementGeneration == existing.PlacementGeneration {
			if session.ComputerID != existing.ComputerID {
				return errors.New("runtime session computer conflicts with current binding")
			}
			if session.UpdatedAt.Before(existing.UpdatedAt) || session.ExpiresAt.Before(existing.ExpiresAt) {
				return errors.New("runtime session response is stale")
			}
			if session.UpdatedAt.Equal(existing.UpdatedAt) && session.ExpiresAt.Equal(existing.ExpiresAt) && session.Token != existing.Token {
				return errors.New("runtime session response conflicts with current token")
			}
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE runtime_sessions
			SET computer_id = ?, placement_generation = ?, token = ?, expires_at = ?, updated_at = ?
			WHERE agent_id = ? AND placement_generation = ?
		`, session.ComputerID, session.PlacementGeneration, session.Token, unixNano(session.ExpiresAt), unixNano(session.UpdatedAt), session.AgentID, existing.PlacementGeneration)
		if err != nil {
			return fmt.Errorf("update runtime session: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return errors.New("runtime session binding changed during update")
		}
	} else if _, err := tx.ExecContext(ctx, `
		INSERT INTO runtime_sessions(agent_id, computer_id, placement_generation, token, expires_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, session.AgentID, session.ComputerID, session.PlacementGeneration, session.Token, unixNano(session.ExpiresAt), unixNano(session.UpdatedAt)); err != nil {
		return fmt.Errorf("insert runtime session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime session: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) DeleteRuntimeSession(ctx context.Context, agentID, computerID string, placementGeneration uint64) error {
	if !validID(agentID) || !validID(computerID) || placementGeneration == 0 {
		return errors.New("runtime session binding is invalid")
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM runtime_sessions
		WHERE agent_id = ? AND computer_id = ? AND placement_generation = ?
	`, agentID, computerID, placementGeneration); err != nil {
		return fmt.Errorf("delete runtime session: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) RuntimeSessions(ctx context.Context) ([]RuntimeSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, computer_id, placement_generation, token, expires_at, updated_at
		FROM runtime_sessions ORDER BY agent_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list runtime sessions: %w", err)
	}
	defer rows.Close()
	var sessions []RuntimeSession
	for rows.Next() {
		var session RuntimeSession
		var expiresAt, updatedAt int64
		if err := rows.Scan(&session.AgentID, &session.ComputerID, &session.PlacementGeneration, &session.Token, &expiresAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan runtime session: %w", err)
		}
		session.ExpiresAt = fromUnixNano(expiresAt)
		session.UpdatedAt = fromUnixNano(updatedAt)
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime sessions: %w", err)
	}
	return sessions, nil
}

func (s *State) RuntimeSession(ctx context.Context, agentID string) (RuntimeSession, bool, error) {
	if !validID(agentID) {
		return RuntimeSession{}, false, errors.New("agent id is invalid")
	}
	return readRuntimeSession(s.db.QueryRowContext(ctx, `
		SELECT agent_id, computer_id, placement_generation, token, expires_at, updated_at
		FROM runtime_sessions WHERE agent_id = ?
	`, agentID))
}

func readRuntimeSession(row interface{ Scan(...any) error }) (RuntimeSession, bool, error) {
	var session RuntimeSession
	var expiresAt, updatedAt int64
	err := row.Scan(&session.AgentID, &session.ComputerID, &session.PlacementGeneration, &session.Token, &expiresAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeSession{}, false, nil
	}
	if err != nil {
		return RuntimeSession{}, false, fmt.Errorf("read runtime session: %w", err)
	}
	session.ExpiresAt = fromUnixNano(expiresAt)
	session.UpdatedAt = fromUnixNano(updatedAt)
	return session, true, nil
}
