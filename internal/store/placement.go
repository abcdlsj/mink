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

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
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

	var profileRevision uint64
	var runtimeSpecRevision sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT current_profile_revision, current_runtime_spec_revision FROM agents WHERE id = ?", params.AgentID).Scan(&profileRevision, &runtimeSpecRevision); errors.Is(err, sql.ErrNoRows) {
		return AgentPlacement{}, placementapp.ErrAgentNotFound
	} else if err != nil {
		return AgentPlacement{}, fmt.Errorf("read agent revisions for placement: %w", err)
	}
	if !runtimeSpecRevision.Valid {
		return AgentPlacement{}, placementapp.ErrRuntimeSpecMissing
	}
	if exists, err := computerExists(ctx, tx, params.ComputerID); err != nil {
		return AgentPlacement{}, err
	} else if !exists {
		return AgentPlacement{}, ErrComputerNotFound
	}
	if err := requirePlacementCredentialBinding(ctx, tx, params.AgentID, params.ComputerID, uint64(runtimeSpecRevision.Int64)); err != nil {
		return AgentPlacement{}, err
	}

	current, err := placementByAgent(ctx, tx, params.AgentID)
	if errors.Is(err, sql.ErrNoRows) {
		stamp := unixNano(params.Now)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_placements(agent_id, computer_id, agent_profile_revision, runtime_spec_revision, desired_revision, state, error_code, created_at, updated_at)
			VALUES(?, ?, ?, ?, 1, 'pending', '', ?, ?)
		`, params.AgentID, params.ComputerID, profileRevision, runtimeSpecRevision.Int64, stamp, stamp); err != nil {
			return AgentPlacement{}, fmt.Errorf("persist placement: %w", err)
		}
		current, err = placementByAgent(ctx, tx, params.AgentID)
	} else if err == nil && (current.ComputerID != params.ComputerID || current.AgentProfileRevision != profileRevision || current.RuntimeSpec.Revision != uint64(runtimeSpecRevision.Int64) || (current.State != placementdomain.StatePending && current.State != placementdomain.StateReady)) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_placements
			SET computer_id = ?, agent_profile_revision = ?, runtime_spec_revision = ?, desired_revision = desired_revision + 1, state = 'pending', error_code = '', updated_at = max(updated_at, ?)
			WHERE agent_id = ?
		`, params.ComputerID, profileRevision, runtimeSpecRevision.Int64, unixNano(params.Now), params.AgentID); err != nil {
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

func requirePlacementCredentialBinding(ctx context.Context, tx *sql.Tx, agentID, computerID string, runtimeSpecRevision uint64) error {
	var engine agentapp.EngineKind
	var protocol agentapp.ProviderProtocol
	var handle string
	if err := tx.QueryRowContext(ctx, `
		SELECT engine, provider_protocol, credential_binding_handle
		FROM agent_runtime_specs
		WHERE agent_id = ? AND revision = ?
	`, agentID, runtimeSpecRevision).Scan(&engine, &protocol, &handle); err != nil {
		return fmt.Errorf("read runtime credential requirement: %w", err)
	}
	kind, ok := runtimeCredentialKind(engine, protocol)
	if !ok {
		return placementapp.ErrCredentialBindingInvalid
	}
	var matches bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM credential_bindings
			WHERE handle = ? AND agent_id = ? AND computer_id = ? AND credential_kind = ?
		)
	`, handle, agentID, computerID, kind).Scan(&matches); err != nil {
		return fmt.Errorf("validate runtime credential binding: %w", err)
	}
	if !matches {
		return placementapp.ErrCredentialBindingInvalid
	}
	return nil
}

func runtimeCredentialKind(engine agentapp.EngineKind, protocol agentapp.ProviderProtocol) (string, bool) {
	switch engine {
	case agentapp.EngineBuiltin:
		switch protocol {
		case agentapp.ProviderOpenAIResponses:
			return "openai", true
		case agentapp.ProviderAnthropicMessages:
			return "anthropic", true
		default:
			return "", false
		}
	case agentapp.EngineCodexAdapter:
		return "codex_adapter", true
	case agentapp.EngineClaudeAdapter:
		return "claude_adapter", true
	default:
		return "", false
	}
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
		       agent_profile_revision, runtime_spec_revision, desired_revision, state, error_code, created_at, updated_at
		FROM agent_placement_requests
		WHERE request_id = ?
	`, params.RequestID).Scan(&actor.Kind, &actor.ID, &storedFingerprint, &placement.AgentID,
		&placement.ComputerID, &placement.AgentProfileRevision, &placement.RuntimeSpec.Revision, &placement.DesiredRevision, &placement.State, &placement.ErrorCode,
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
	placement.RuntimeSpec, err = runtimeSpecAtRevision(ctx, tx, placement.AgentID, placement.RuntimeSpec.Revision)
	if err != nil {
		return AgentPlacement{}, false, fmt.Errorf("read placement request runtime spec: %w", err)
	}
	placement.AgentProfile, err = profileAtRevision(ctx, tx, placement.AgentID, placement.AgentProfileRevision)
	if err != nil {
		return AgentPlacement{}, false, fmt.Errorf("read placement request agent profile: %w", err)
	}
	return placement, true, nil
}

func persistPlacementRequest(ctx context.Context, tx *sql.Tx, params SetAgentPlacementParams, fingerprint [sha256.Size]byte, placement AgentPlacement) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_placement_requests(
			request_id, actor_kind, actor_id, payload_fingerprint, agent_id, computer_id,
			agent_profile_revision, runtime_spec_revision, desired_revision, state, error_code, created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, params.RequestID, params.Actor.Kind, params.Actor.ID, fingerprint[:], placement.AgentID,
		placement.ComputerID, placement.AgentProfileRevision, placement.RuntimeSpec.Revision, placement.DesiredRevision, placement.State, placement.ErrorCode,
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
	placement, err := scanPlacement(s.db.QueryRowContext(ctx, placementSelect+" WHERE placements.agent_id = ?", agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentPlacement{}, ErrPlacementNotFound
	}
	if err != nil {
		return AgentPlacement{}, fmt.Errorf("get placement: %w", err)
	}
	return placement, nil
}

func (s *Store) ListAgentPlacements(ctx context.Context) ([]AgentPlacement, error) {
	rows, err := s.db.QueryContext(ctx, placementSelect+" ORDER BY placements.agent_id")
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
	rows, err := tx.QueryContext(ctx, placementSelect+" WHERE placements.computer_id = ? AND placements.state = 'pending' ORDER BY placements.agent_id", params.ComputerID)
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
	rows, err := tx.QueryContext(ctx, placementSelect+" WHERE placements.computer_id = ? ORDER BY placements.agent_id", params.ComputerID)
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
	if current.ComputerID != params.ComputerID || current.DesiredRevision != params.DesiredRevision {
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
		WHERE agent_id = ? AND computer_id = ? AND desired_revision = ? AND state = 'pending'
	`, params.State, params.ErrorCode, unixNano(params.Now), params.AgentID, params.ComputerID, params.DesiredRevision); err != nil {
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
	SELECT placements.agent_id, placements.computer_id, placements.agent_profile_revision,
	       placements.desired_revision, placements.state, placements.error_code,
	       placements.created_at, placements.updated_at,
	       specs.agent_id, specs.revision, specs.engine, specs.provider_protocol,
	       specs.provider_endpoint, specs.model, specs.credential_binding_handle,
	       specs.sandbox_provider, specs.max_run_duration_seconds, specs.max_output_bytes,
	       specs.tool_message, specs.tool_work, specs.tool_artifact, specs.tool_knowledge,
	       specs.created_at,
	       profiles.agent_id, profiles.revision, profiles.display_name, profiles.role,
	       profiles.mission, profiles.instructions, profiles.created_at
	FROM agent_placements AS placements
	JOIN agent_runtime_specs AS specs
	  ON specs.agent_id = placements.agent_id
	 AND specs.revision = placements.runtime_spec_revision
	JOIN agent_profiles AS profiles
	  ON profiles.agent_id = placements.agent_id
	 AND profiles.revision = placements.agent_profile_revision`

func placementByAgent(ctx context.Context, tx *sql.Tx, agentID string) (AgentPlacement, error) {
	return scanPlacement(tx.QueryRowContext(ctx, placementSelect+" WHERE placements.agent_id = ?", agentID))
}

func scanPlacement(row scanner) (AgentPlacement, error) {
	var placement AgentPlacement
	var createdAt, updatedAt, runtimeCreatedAt, profileCreatedAt, maxRunDurationSeconds int64
	if err := row.Scan(
		&placement.AgentID, &placement.ComputerID, &placement.AgentProfileRevision,
		&placement.DesiredRevision, &placement.State, &placement.ErrorCode, &createdAt, &updatedAt,
		&placement.RuntimeSpec.AgentID, &placement.RuntimeSpec.Revision, &placement.RuntimeSpec.Engine,
		&placement.RuntimeSpec.ProviderProtocol, &placement.RuntimeSpec.ProviderEndpoint, &placement.RuntimeSpec.Model,
		&placement.RuntimeSpec.CredentialBindingHandle, &placement.RuntimeSpec.SandboxProvider, &maxRunDurationSeconds,
		&placement.RuntimeSpec.MaxOutputBytes, &placement.RuntimeSpec.ToolPolicy.Message, &placement.RuntimeSpec.ToolPolicy.Work,
		&placement.RuntimeSpec.ToolPolicy.Artifact, &placement.RuntimeSpec.ToolPolicy.Knowledge, &runtimeCreatedAt,
		&placement.AgentProfile.AgentID, &placement.AgentProfile.Revision, &placement.AgentProfile.DisplayName,
		&placement.AgentProfile.Role, &placement.AgentProfile.Mission, &placement.AgentProfile.Instructions, &profileCreatedAt,
	); err != nil {
		return AgentPlacement{}, err
	}
	placement.CreatedAt = timeFromUnixNano(createdAt)
	placement.UpdatedAt = timeFromUnixNano(updatedAt)
	placement.RuntimeSpec.MaxRunDuration = time.Duration(maxRunDurationSeconds) * time.Second
	placement.RuntimeSpec.CreatedAt = timeFromUnixNano(runtimeCreatedAt)
	placement.AgentProfile.CreatedAt = timeFromUnixNano(profileCreatedAt)
	return placement, nil
}

func profileAtRevision(ctx context.Context, tx *sql.Tx, agentID string, revision uint64) (agentapp.Profile, error) {
	var profile agentapp.Profile
	var createdAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT agent_id, revision, display_name, role, mission, instructions, created_at
		FROM agent_profiles WHERE agent_id = ? AND revision = ?
	`, agentID, revision).Scan(
		&profile.AgentID, &profile.Revision, &profile.DisplayName, &profile.Role,
		&profile.Mission, &profile.Instructions, &createdAt,
	); err != nil {
		return agentapp.Profile{}, err
	}
	profile.CreatedAt = timeFromUnixNano(createdAt)
	return profile, nil
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

func authenticateComputer(ctx context.Context, tx *sql.Tx, computerID, rk string) error {
	keyHash := sha256.Sum256([]byte(rk))
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
