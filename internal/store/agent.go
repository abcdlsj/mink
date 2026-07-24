package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	"github.com/google/uuid"
)

type Agent = agentapp.Agent

type CreateAgentParams = agentapp.CreateCommand

type UpdateAgentProfileParams = agentapp.UpdateProfileCommand

const currentAgentSelect = `
	SELECT agents.id, agents.handle, agents.created_at, agents.updated_at,
	       profiles.agent_id, profiles.revision, profiles.display_name, profiles.role,
	       profiles.mission, profiles.instructions, profiles.created_at
	FROM agents
	JOIN agent_profiles AS profiles
	  ON profiles.agent_id = agents.id
	 AND profiles.revision = agents.current_profile_revision`

func (s *Store) CreateAgent(ctx context.Context, params CreateAgentParams) (Agent, error) {
	fingerprint, err := agentCreateFingerprint(params)
	if err != nil {
		return Agent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, fmt.Errorf("begin agent creation: %w", err)
	}
	defer tx.Rollback()
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityAgentCreate, Scope{Kind: "organization", ID: params.Actor.OrganizationID}, params.Now, ""); err != nil {
		return Agent{}, err
	} else if reason != "" {
		return Agent{}, commitDenied(ctx, tx, params.Actor, AuditAgentCreate, "agent", "", params.RequestID, reason, params.Now)
	}

	var existingID string
	var existingActor Principal
	var storedFingerprint []byte
	err = tx.QueryRowContext(ctx, `
		SELECT actor_kind, actor_id, agent_id, payload_fingerprint
		FROM agent_create_requests
		WHERE request_id = ?
	`, params.RequestID).Scan(&existingActor.Kind, &existingActor.ID, &existingID, &storedFingerprint)
	if err == nil {
		if existingActor.Kind != params.Actor.Kind || existingActor.ID != params.Actor.ID || !bytes.Equal(storedFingerprint, fingerprint[:]) {
			return Agent{}, ErrAgentRequestConflict
		}
		existing, err := agentAtProfileRevision(ctx, tx, existingID, 1)
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

	var handleExists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE handle = ?)", params.Handle).Scan(&handleExists); err != nil {
		return Agent{}, fmt.Errorf("check agent handle: %w", err)
	}
	if handleExists {
		return Agent{}, ErrAgentHandleExists
	}

	agent := Agent{
		ID: uuid.NewString(), Handle: params.Handle, CreatedAt: params.Now.UTC(), UpdatedAt: params.Now.UTC(),
		Profile: agentapp.Profile{
			Revision: 1, DisplayName: params.DisplayName, Role: params.Role,
			Mission: params.Mission, Instructions: params.Instructions, CreatedAt: params.Now.UTC(),
		},
	}
	agent.Profile.AgentID = agent.ID
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents(id, handle, current_profile_revision, created_at, updated_at)
		VALUES(?, ?, 1, ?, ?)
	`, agent.ID, agent.Handle, unixNano(agent.CreatedAt), unixNano(agent.UpdatedAt)); err != nil {
		if isUniqueConstraint(err, "agents.handle") {
			return Agent{}, ErrAgentHandleExists
		}
		return Agent{}, fmt.Errorf("persist agent: %w", err)
	}
	if err := insertAgentProfile(ctx, tx, agent.Profile); err != nil {
		return Agent{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_create_requests(request_id, actor_kind, actor_id, agent_id, payload_fingerprint)
		VALUES(?, ?, ?, ?, ?)
	`, params.RequestID, params.Actor.Kind, params.Actor.ID, agent.ID, fingerprint[:]); err != nil {
		return Agent{}, fmt.Errorf("persist agent creation request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditAgentCreate,
		TargetKind: "agent", TargetID: agent.ID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now,
	}); err != nil {
		return Agent{}, err
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, fmt.Errorf("commit agent creation: %w", err)
	}
	return agent, nil
}

func (s *Store) UpdateAgentProfile(ctx context.Context, params UpdateAgentProfileParams) (Agent, error) {
	fingerprint, err := agentProfileFingerprint(params)
	if err != nil {
		return Agent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, fmt.Errorf("begin agent profile update: %w", err)
	}
	defer tx.Rollback()
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityAgentProfileUpdate, Scope{Kind: "agent", ID: params.AgentID}, params.Now, ""); err != nil {
		return Agent{}, err
	} else if reason != "" {
		return Agent{}, commitDenied(ctx, tx, params.Actor, AuditAgentProfileUpdate, "agent", params.AgentID, params.RequestID, reason, params.Now)
	}

	var receiptActor Principal
	var receiptAgentID string
	var storedFingerprint []byte
	var resultRevision uint64
	err = tx.QueryRowContext(ctx, `
		SELECT actor_kind, actor_id, agent_id, payload_fingerprint, result_revision
		FROM agent_profile_update_requests
		WHERE request_id = ?
	`, params.RequestID).Scan(&receiptActor.Kind, &receiptActor.ID, &receiptAgentID, &storedFingerprint, &resultRevision)
	if err == nil {
		if receiptActor.Kind != params.Actor.Kind || receiptActor.ID != params.Actor.ID || receiptAgentID != params.AgentID || !bytes.Equal(storedFingerprint, fingerprint[:]) {
			return Agent{}, ErrAgentRequestConflict
		}
		replayed, err := agentAtProfileRevision(ctx, tx, params.AgentID, resultRevision)
		if err != nil {
			return Agent{}, fmt.Errorf("read idempotent agent profile: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Agent{}, fmt.Errorf("commit idempotent profile update: %w", err)
		}
		return replayed, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Agent{}, fmt.Errorf("read agent profile update request: %w", err)
	}

	current, err := currentAgent(ctx, tx, params.AgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrAgentNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("read agent for profile update: %w", err)
	}
	if current.Profile.Revision != params.ExpectedRevision {
		return Agent{}, ErrAgentRevisionConflict
	}
	profile := agentapp.Profile{
		AgentID: params.AgentID, Revision: params.ExpectedRevision + 1, DisplayName: params.DisplayName,
		Role: params.Role, Mission: params.Mission, Instructions: params.Instructions, CreatedAt: params.Now.UTC(),
	}
	if err := insertAgentProfile(ctx, tx, profile); err != nil {
		return Agent{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE agents
		SET current_profile_revision = ?, updated_at = max(updated_at, ?)
		WHERE id = ? AND current_profile_revision = ?
	`, profile.Revision, unixNano(params.Now), params.AgentID, params.ExpectedRevision)
	if err != nil {
		return Agent{}, fmt.Errorf("advance agent profile revision: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return Agent{}, ErrAgentRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_placements
		SET agent_profile_revision = ?, desired_revision = desired_revision + 1,
		    state = 'pending', error_code = '', updated_at = max(updated_at, ?)
		WHERE agent_id = ?
	`, profile.Revision, unixNano(params.Now), params.AgentID); err != nil {
		return Agent{}, fmt.Errorf("reconcile placement profile revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_profile_update_requests(
			request_id, actor_kind, actor_id, agent_id, expected_revision, result_revision, payload_fingerprint
		) VALUES(?, ?, ?, ?, ?, ?, ?)
	`, params.RequestID, params.Actor.Kind, params.Actor.ID, params.AgentID, params.ExpectedRevision, profile.Revision, fingerprint[:]); err != nil {
		return Agent{}, fmt.Errorf("persist agent profile update request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditAgentProfileUpdate,
		TargetKind: "agent", TargetID: params.AgentID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now,
	}); err != nil {
		return Agent{}, err
	}
	agent, err := currentAgent(ctx, tx, params.AgentID)
	if err != nil {
		return Agent{}, fmt.Errorf("read updated agent profile: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, fmt.Errorf("commit agent profile update: %w", err)
	}
	return agent, nil
}

func (s *Store) GetAgent(ctx context.Context, id string) (Agent, error) {
	agent, err := scanAgent(s.db.QueryRowContext(ctx, currentAgentSelect+" WHERE agents.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrAgentNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("get agent: %w", err)
	}
	return agent, nil
}

func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, currentAgentSelect+" ORDER BY agents.handle, agents.id")
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

func currentAgent(ctx context.Context, tx *sql.Tx, agentID string) (Agent, error) {
	return scanAgent(tx.QueryRowContext(ctx, currentAgentSelect+" WHERE agents.id = ?", agentID))
}

func agentAtProfileRevision(ctx context.Context, tx *sql.Tx, agentID string, revision uint64) (Agent, error) {
	return scanAgent(tx.QueryRowContext(ctx, `
		SELECT agents.id, agents.handle, agents.created_at, profiles.created_at,
		       profiles.agent_id, profiles.revision, profiles.display_name, profiles.role,
		       profiles.mission, profiles.instructions, profiles.created_at
		FROM agents
		JOIN agent_profiles AS profiles ON profiles.agent_id = agents.id
		WHERE agents.id = ? AND profiles.revision = ?
	`, agentID, revision))
}

func scanAgent(row scanner) (Agent, error) {
	var agent Agent
	var createdAt, updatedAt, profileCreatedAt int64
	if err := row.Scan(
		&agent.ID, &agent.Handle, &createdAt, &updatedAt,
		&agent.Profile.AgentID, &agent.Profile.Revision, &agent.Profile.DisplayName, &agent.Profile.Role,
		&agent.Profile.Mission, &agent.Profile.Instructions, &profileCreatedAt,
	); err != nil {
		return Agent{}, err
	}
	agent.CreatedAt = timeFromUnixNano(createdAt)
	agent.UpdatedAt = timeFromUnixNano(updatedAt)
	agent.Profile.CreatedAt = timeFromUnixNano(profileCreatedAt)
	return agent, nil
}

func insertAgentProfile(ctx context.Context, tx *sql.Tx, profile agentapp.Profile) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_profiles(agent_id, revision, display_name, role, mission, instructions, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, profile.AgentID, profile.Revision, profile.DisplayName, profile.Role, profile.Mission, profile.Instructions, unixNano(profile.CreatedAt)); err != nil {
		return fmt.Errorf("persist agent profile: %w", err)
	}
	return nil
}

func agentCreateFingerprint(params CreateAgentParams) ([sha256.Size]byte, error) {
	return agentFingerprint(struct {
		ActorKind      PrincipalKind `json:"actor_kind"`
		ActorID        string        `json:"actor_id"`
		OrganizationID string        `json:"organization_id"`
		Handle         string        `json:"handle"`
		DisplayName    string        `json:"display_name"`
		Role           string        `json:"role"`
		Mission        string        `json:"mission"`
		Instructions   string        `json:"instructions"`
	}{params.Actor.Kind, params.Actor.ID, params.Actor.OrganizationID, params.Handle, params.DisplayName, params.Role, params.Mission, params.Instructions})
}

func agentProfileFingerprint(params UpdateAgentProfileParams) ([sha256.Size]byte, error) {
	return agentFingerprint(struct {
		ActorKind        PrincipalKind `json:"actor_kind"`
		ActorID          string        `json:"actor_id"`
		OrganizationID   string        `json:"organization_id"`
		AgentID          string        `json:"agent_id"`
		ExpectedRevision uint64        `json:"expected_revision"`
		DisplayName      string        `json:"display_name"`
		Role             string        `json:"role"`
		Mission          string        `json:"mission"`
		Instructions     string        `json:"instructions"`
	}{params.Actor.Kind, params.Actor.ID, params.Actor.OrganizationID, params.AgentID, params.ExpectedRevision, params.DisplayName, params.Role, params.Mission, params.Instructions})
}

func agentFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode agent request payload: %w", err)
	}
	return sha256.Sum256(payload), nil
}
