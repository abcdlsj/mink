package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
	placementdomain "github.com/abcdlsj/sumi/internal/placement/domain"
)

type AgentPlacement = placementapp.Placement

type AcknowledgePlacementParams = placementapp.AcknowledgeCommand

type SetAgentPlacementParams = placementapp.SetCommand

type ComputerPlacementReadParams = placementapp.ComputerReadQuery

func (s *Store) SetAgentPlacement(ctx context.Context, params SetAgentPlacementParams) (AgentPlacement, error) {
	fingerprint, err := placementRequestFingerprint(params)
	if err != nil {
		return AgentPlacement{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("begin set placement: %w", err)
	}
	defer tx.Rollback()
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityAgentPlace, Scope{Kind: "agent", ID: params.AgentID}, params.Now, ""); err != nil {
		return AgentPlacement{}, err
	} else if reason != "" {
		return AgentPlacement{}, commitDeniedWithContext(ctx, tx, params.Actor, AuditAgentPlace, "agent", params.AgentID, "computer", params.ComputerID, params.RequestID, reason, params.Now)
	}
	if placement, found, err := readPlacementRequest(ctx, tx, params, fingerprint); err != nil || found {
		return commitPlacementReplay(tx, placement, found, err)
	}

	if exists, err := agentExists(ctx, tx, params.AgentID); err != nil {
		return AgentPlacement{}, err
	} else if !exists {
		return AgentPlacement{}, placementapp.ErrAgentNotFound
	}
	if exists, err := computerExists(ctx, tx, params.ComputerID); err != nil {
		return AgentPlacement{}, err
	} else if !exists {
		return AgentPlacement{}, ErrComputerNotFound
	}

	current, err := placementByAgent(ctx, tx, params.AgentID)
	if errors.Is(err, sql.ErrNoRows) {
		stamp := unixNano(params.Now)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_placements(agent_id, computer_id, generation, state, error_code, created_at, updated_at)
			VALUES(?, ?, 1, 'pending', '', ?, ?)
		`, params.AgentID, params.ComputerID, stamp, stamp); err != nil {
			return AgentPlacement{}, fmt.Errorf("persist placement: %w", err)
		}
		current, err = placementByAgent(ctx, tx, params.AgentID)
	} else if err == nil && (current.ComputerID != params.ComputerID || (current.State != placementdomain.StatePending && current.State != placementdomain.StateActive)) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_placements
			SET computer_id = ?, generation = generation + 1, state = 'pending', error_code = '', updated_at = max(updated_at, ?)
			WHERE agent_id = ?
		`, params.ComputerID, unixNano(params.Now), params.AgentID); err != nil {
			return AgentPlacement{}, fmt.Errorf("replace placement: %w", err)
		}
		current, err = placementByAgent(ctx, tx, params.AgentID)
	}
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("read placement after set: %w", err)
	}
	if err := persistPlacementRequest(ctx, tx, params, fingerprint, current); err != nil {
		return AgentPlacement{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID,
		Actor:          params.Actor,
		Action:         AuditAgentPlace,
		TargetKind:     "agent",
		TargetID:       params.AgentID,
		ContextKind:    "computer",
		ContextID:      params.ComputerID,
		RequestID:      params.RequestID,
		Outcome:        "committed",
		Now:            params.Now,
	}); err != nil {
		return AgentPlacement{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentPlacement{}, fmt.Errorf("commit set placement: %w", err)
	}
	return current, nil
}

func placementRequestFingerprint(params SetAgentPlacementParams) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		ActorKind      PrincipalKind `json:"actor_kind"`
		ActorID        string        `json:"actor_id"`
		OrganizationID string        `json:"organization_id"`
		AgentID        string        `json:"agent_id"`
		ComputerID     string        `json:"computer_id"`
	}{params.Actor.Kind, params.Actor.ID, params.Actor.OrganizationID, params.AgentID, params.ComputerID})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode placement request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func readPlacementRequest(ctx context.Context, tx *sql.Tx, params SetAgentPlacementParams, fingerprint [sha256.Size]byte) (AgentPlacement, bool, error) {
	var placement AgentPlacement
	var actor Principal
	var storedFingerprint []byte
	var createdAt, updatedAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT actor_kind, actor_id, payload_fingerprint, agent_id, computer_id,
		       generation, state, error_code, created_at, updated_at
		FROM agent_placement_requests
		WHERE request_id = ?
	`, params.RequestID).Scan(&actor.Kind, &actor.ID, &storedFingerprint, &placement.AgentID,
		&placement.ComputerID, &placement.Generation, &placement.State, &placement.ErrorCode,
		&createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentPlacement{}, false, nil
	}
	if err != nil {
		return AgentPlacement{}, false, fmt.Errorf("read placement request: %w", err)
	}
	if actor.Kind != params.Actor.Kind || actor.ID != params.Actor.ID || !bytes.Equal(storedFingerprint, fingerprint[:]) {
		return AgentPlacement{}, false, ErrPlacementRequestConflict
	}
	placement.CreatedAt = timeFromUnixNano(createdAt)
	placement.UpdatedAt = timeFromUnixNano(updatedAt)
	return placement, true, nil
}

func persistPlacementRequest(ctx context.Context, tx *sql.Tx, params SetAgentPlacementParams, fingerprint [sha256.Size]byte, placement AgentPlacement) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_placement_requests(
			request_id, actor_kind, actor_id, payload_fingerprint, agent_id, computer_id,
			generation, state, error_code, created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, params.RequestID, params.Actor.Kind, params.Actor.ID, fingerprint[:], placement.AgentID,
		placement.ComputerID, placement.Generation, placement.State, placement.ErrorCode,
		unixNano(placement.CreatedAt), unixNano(placement.UpdatedAt)); err != nil {
		return fmt.Errorf("persist placement request: %w", err)
	}
	return nil
}

func commitPlacementReplay(tx *sql.Tx, placement AgentPlacement, found bool, err error) (AgentPlacement, error) {
	if err != nil {
		return AgentPlacement{}, err
	}
	if !found {
		return AgentPlacement{}, errors.New("placement replay called without receipt")
	}
	if err := tx.Commit(); err != nil {
		return AgentPlacement{}, fmt.Errorf("commit placement request replay: %w", err)
	}
	return placement, nil
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

func (s *Store) ListComputerAssignments(ctx context.Context, params ComputerPlacementReadParams) ([]AgentPlacement, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list assignments: %w", err)
	}
	defer tx.Rollback()
	if err := authenticateComputer(ctx, tx, params.ComputerID, params.RegistrationKey); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, placementSelect+" WHERE computer_id = ? AND state = 'pending' ORDER BY agent_id", params.ComputerID)
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

func (s *Store) ListComputerPlacements(ctx context.Context, params ComputerPlacementReadParams) ([]AgentPlacement, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list computer placements: %w", err)
	}
	defer tx.Rollback()
	if err := authenticateComputer(ctx, tx, params.ComputerID, params.RegistrationKey); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, placementSelect+" WHERE computer_id = ? ORDER BY agent_id", params.ComputerID)
	if err != nil {
		return nil, fmt.Errorf("list computer placements: %w", err)
	}
	placements, err := scanPlacements(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit list computer placements: %w", err)
	}
	return placements, nil
}

func (s *Store) AcknowledgeAgentPlacement(ctx context.Context, params AcknowledgePlacementParams) (AgentPlacement, error) {
	if _, err := placementdomain.NewAcknowledgement(params.State, params.ErrorCode); err != nil {
		return AgentPlacement{}, err
	}
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
	if current.State != placementdomain.StatePending {
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
