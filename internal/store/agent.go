package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Agent struct {
	ID          string
	Name        string
	Description string
	Driver      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateAgentParams struct {
	RequestID   string
	Name        string
	Description string
	Driver      string
	Now         time.Time
}

func (s *Store) CreateAgent(ctx context.Context, params CreateAgentParams) (Agent, error) {
	fingerprint, err := agentPayloadFingerprint(params)
	if err != nil {
		return Agent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, fmt.Errorf("begin agent creation: %w", err)
	}
	defer tx.Rollback()

	var existingID string
	var existingFingerprint []byte
	err = tx.QueryRowContext(ctx, `
		SELECT agent_id, payload_fingerprint
		FROM agent_create_requests
		WHERE request_id = ?
	`, params.RequestID).Scan(&existingID, &existingFingerprint)
	if err == nil {
		if !bytes.Equal(existingFingerprint, fingerprint[:]) {
			return Agent{}, ErrAgentRequestConflict
		}
		existing, err := scanAgent(tx.QueryRowContext(ctx, `
			SELECT id, name, description, driver, created_at, updated_at
			FROM agents
			WHERE id = ?
		`, existingID))
		if err != nil {
			return Agent{}, fmt.Errorf("read idempotent agent: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Agent{}, fmt.Errorf("commit idempotent agent creation: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Agent{}, fmt.Errorf("read agent creation request: %w", err)
	}

	var nameExists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE name = ?)", params.Name).Scan(&nameExists); err != nil {
		return Agent{}, fmt.Errorf("check agent name: %w", err)
	}
	if nameExists {
		return Agent{}, ErrAgentNameExists
	}

	stamp := unixNano(params.Now)
	agent := Agent{
		ID:          uuid.NewString(),
		Name:        params.Name,
		Description: params.Description,
		Driver:      params.Driver,
		CreatedAt:   params.Now.UTC(),
		UpdatedAt:   params.Now.UTC(),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents(id, name, description, driver, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, agent.ID, agent.Name, agent.Description, agent.Driver, stamp, stamp); err != nil {
		return Agent{}, fmt.Errorf("persist agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_create_requests(request_id, agent_id, payload_fingerprint)
		VALUES(?, ?, ?)
	`, params.RequestID, agent.ID, fingerprint[:]); err != nil {
		return Agent{}, fmt.Errorf("persist agent creation request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, fmt.Errorf("commit agent creation: %w", err)
	}
	return agent, nil
}

func (s *Store) GetAgent(ctx context.Context, id string) (Agent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, driver, created_at, updated_at
		FROM agents
		WHERE id = ?
	`, id)
	agent, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrAgentNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("get agent: %w", err)
	}
	return agent, nil
}

func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, driver, created_at, updated_at
		FROM agents
		ORDER BY name, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents: %w", err)
	}
	return agents, nil
}

func scanAgent(row scanner) (Agent, error) {
	var agent Agent
	var createdAt, updatedAt int64
	if err := row.Scan(&agent.ID, &agent.Name, &agent.Description, &agent.Driver, &createdAt, &updatedAt); err != nil {
		return Agent{}, err
	}
	agent.CreatedAt = timeFromUnixNano(createdAt)
	agent.UpdatedAt = timeFromUnixNano(updatedAt)
	return agent, nil
}

func agentPayloadFingerprint(params CreateAgentParams) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Driver      string `json:"driver"`
	}{
		Name:        params.Name,
		Description: params.Description,
		Driver:      params.Driver,
	})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode agent request payload: %w", err)
	}
	return sha256.Sum256(payload), nil
}
