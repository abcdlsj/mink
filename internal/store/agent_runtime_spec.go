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
)

type AgentRuntimeSpec = agentapp.RuntimeSpec

type UpdateAgentRuntimeSpecParams = agentapp.UpdateRuntimeSpecCommand

const runtimeSpecSelect = `
	SELECT agent_id, revision, engine, provider_protocol, provider_endpoint, model,
	       credential_binding_handle, sandbox_provider, max_run_duration_seconds,
	       max_output_bytes, tool_message, tool_work, tool_artifact, tool_knowledge, created_at
	FROM agent_runtime_specs`

func (s *Store) UpdateAgentRuntimeSpec(ctx context.Context, params UpdateAgentRuntimeSpecParams) (AgentRuntimeSpec, error) {
	fingerprint, err := runtimeSpecFingerprint(params)
	if err != nil {
		return AgentRuntimeSpec{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRuntimeSpec{}, fmt.Errorf("begin agent runtime spec update: %w", err)
	}
	defer tx.Rollback()
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityAgentRuntimeConfigure, Scope{Kind: "agent", ID: params.AgentID}, params.Now, ""); err != nil {
		return AgentRuntimeSpec{}, err
	} else if reason != "" {
		return AgentRuntimeSpec{}, commitDenied(ctx, tx, params.Actor, AuditAgentRuntimeConfigure, "agent", params.AgentID, params.RequestID, reason, params.Now)
	}

	var receiptActor Principal
	var receiptAgentID string
	var storedFingerprint []byte
	var resultRevision uint64
	err = tx.QueryRowContext(ctx, `
		SELECT actor_kind, actor_id, agent_id, payload_fingerprint, result_revision
		FROM agent_runtime_spec_update_requests
		WHERE request_id = ?
	`, params.RequestID).Scan(&receiptActor.Kind, &receiptActor.ID, &receiptAgentID, &storedFingerprint, &resultRevision)
	if err == nil {
		if receiptActor.Kind != params.Actor.Kind || receiptActor.ID != params.Actor.ID || receiptAgentID != params.AgentID || !bytes.Equal(storedFingerprint, fingerprint[:]) {
			return AgentRuntimeSpec{}, ErrAgentRequestConflict
		}
		replayed, err := runtimeSpecAtRevision(ctx, tx, params.AgentID, resultRevision)
		if err != nil {
			return AgentRuntimeSpec{}, fmt.Errorf("read idempotent runtime spec: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return AgentRuntimeSpec{}, fmt.Errorf("commit idempotent runtime spec update: %w", err)
		}
		return replayed, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AgentRuntimeSpec{}, fmt.Errorf("read runtime spec update request: %w", err)
	}

	var current sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT current_runtime_spec_revision FROM agents WHERE id = ?", params.AgentID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return AgentRuntimeSpec{}, ErrAgentNotFound
	} else if err != nil {
		return AgentRuntimeSpec{}, fmt.Errorf("read agent runtime spec revision: %w", err)
	}
	currentRevision := uint64(0)
	if current.Valid {
		currentRevision = uint64(current.Int64)
	}
	if currentRevision != params.ExpectedRevision {
		return AgentRuntimeSpec{}, ErrAgentRuntimeSpecRevisionConflict
	}

	spec := AgentRuntimeSpec{
		AgentID: params.AgentID, Revision: currentRevision + 1, Engine: params.Engine,
		ProviderProtocol: params.ProviderProtocol, ProviderEndpoint: params.ProviderEndpoint, Model: params.Model,
		CredentialBindingHandle: params.CredentialBindingHandle, SandboxProvider: params.SandboxProvider,
		MaxRunDuration: params.MaxRunDuration, MaxOutputBytes: params.MaxOutputBytes,
		ToolPolicy: params.ToolPolicy, CreatedAt: params.Now.UTC(),
	}
	if err := insertRuntimeSpec(ctx, tx, spec); err != nil {
		return AgentRuntimeSpec{}, err
	}
	var result sql.Result
	if current.Valid {
		result, err = tx.ExecContext(ctx, `
			UPDATE agents SET current_runtime_spec_revision = ?, updated_at = max(updated_at, ?)
			WHERE id = ? AND current_runtime_spec_revision = ?
		`, spec.Revision, unixNano(params.Now), params.AgentID, currentRevision)
	} else {
		result, err = tx.ExecContext(ctx, `
			UPDATE agents SET current_runtime_spec_revision = ?, updated_at = max(updated_at, ?)
			WHERE id = ? AND current_runtime_spec_revision IS NULL
		`, spec.Revision, unixNano(params.Now), params.AgentID)
	}
	if err != nil {
		return AgentRuntimeSpec{}, fmt.Errorf("advance agent runtime spec revision: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return AgentRuntimeSpec{}, ErrAgentRuntimeSpecRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_placements
		SET runtime_spec_revision = ?, desired_revision = desired_revision + 1,
		    state = 'pending', error_code = '', updated_at = max(updated_at, ?)
		WHERE agent_id = ?
	`, spec.Revision, unixNano(params.Now), params.AgentID); err != nil {
		return AgentRuntimeSpec{}, fmt.Errorf("reconcile placement runtime spec revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_runtime_spec_update_requests(
			request_id, actor_kind, actor_id, agent_id, expected_revision, result_revision, payload_fingerprint
		) VALUES(?, ?, ?, ?, ?, ?, ?)
	`, params.RequestID, params.Actor.Kind, params.Actor.ID, params.AgentID, params.ExpectedRevision, spec.Revision, fingerprint[:]); err != nil {
		return AgentRuntimeSpec{}, fmt.Errorf("persist runtime spec update request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditAgentRuntimeConfigure,
		TargetKind: "agent", TargetID: params.AgentID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now,
	}); err != nil {
		return AgentRuntimeSpec{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRuntimeSpec{}, fmt.Errorf("commit agent runtime spec update: %w", err)
	}
	return spec, nil
}

func (s *Store) GetAgentRuntimeSpec(ctx context.Context, agentID string) (AgentRuntimeSpec, error) {
	spec, err := scanRuntimeSpec(s.db.QueryRowContext(ctx, runtimeSpecSelect+`
		WHERE agent_id = ? AND revision = (
			SELECT current_runtime_spec_revision FROM agents WHERE id = ?
		)
	`, agentID, agentID))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if checkErr := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE id = ?)", agentID).Scan(&exists); checkErr != nil {
			return AgentRuntimeSpec{}, fmt.Errorf("check agent for runtime spec: %w", checkErr)
		}
		if !exists {
			return AgentRuntimeSpec{}, ErrAgentNotFound
		}
		return AgentRuntimeSpec{}, ErrAgentRuntimeSpecMissing
	}
	if err != nil {
		return AgentRuntimeSpec{}, fmt.Errorf("get agent runtime spec: %w", err)
	}
	return spec, nil
}

func runtimeSpecAtRevision(ctx context.Context, tx *sql.Tx, agentID string, revision uint64) (AgentRuntimeSpec, error) {
	return scanRuntimeSpec(tx.QueryRowContext(ctx, runtimeSpecSelect+" WHERE agent_id = ? AND revision = ?", agentID, revision))
}

func insertRuntimeSpec(ctx context.Context, tx *sql.Tx, spec AgentRuntimeSpec) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_runtime_specs(
			agent_id, revision, engine, provider_protocol, provider_endpoint, model,
			credential_binding_handle, sandbox_provider, max_run_duration_seconds,
			max_output_bytes, tool_message, tool_work, tool_artifact, tool_knowledge, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, spec.AgentID, spec.Revision, spec.Engine, spec.ProviderProtocol, spec.ProviderEndpoint, spec.Model,
		spec.CredentialBindingHandle, spec.SandboxProvider, int64(spec.MaxRunDuration/time.Second),
		spec.MaxOutputBytes, spec.ToolPolicy.Message, spec.ToolPolicy.Work, spec.ToolPolicy.Artifact,
		spec.ToolPolicy.Knowledge, unixNano(spec.CreatedAt)); err != nil {
		return fmt.Errorf("persist agent runtime spec: %w", err)
	}
	return nil
}

func scanRuntimeSpec(row scanner) (AgentRuntimeSpec, error) {
	var spec AgentRuntimeSpec
	var maxRunDurationSeconds, createdAt int64
	if err := row.Scan(
		&spec.AgentID, &spec.Revision, &spec.Engine, &spec.ProviderProtocol, &spec.ProviderEndpoint,
		&spec.Model, &spec.CredentialBindingHandle, &spec.SandboxProvider, &maxRunDurationSeconds,
		&spec.MaxOutputBytes, &spec.ToolPolicy.Message, &spec.ToolPolicy.Work, &spec.ToolPolicy.Artifact,
		&spec.ToolPolicy.Knowledge, &createdAt,
	); err != nil {
		return AgentRuntimeSpec{}, err
	}
	spec.MaxRunDuration = time.Duration(maxRunDurationSeconds) * time.Second
	spec.CreatedAt = timeFromUnixNano(createdAt)
	return spec, nil
}

func runtimeSpecFingerprint(params UpdateAgentRuntimeSpecParams) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		ActorKind               PrincipalKind             `json:"actor_kind"`
		ActorID                 string                    `json:"actor_id"`
		OrganizationID          string                    `json:"organization_id"`
		AgentID                 string                    `json:"agent_id"`
		ExpectedRevision        uint64                    `json:"expected_revision"`
		Engine                  agentapp.EngineKind       `json:"engine"`
		ProviderProtocol        agentapp.ProviderProtocol `json:"provider_protocol"`
		ProviderEndpoint        string                    `json:"provider_endpoint"`
		Model                   string                    `json:"model"`
		CredentialBindingHandle string                    `json:"credential_binding_handle"`
		SandboxProvider         string                    `json:"sandbox_provider"`
		MaxRunDurationNanos     int64                     `json:"max_run_duration_nanos"`
		MaxOutputBytes          uint64                    `json:"max_output_bytes"`
		ToolPolicy              agentapp.ToolPolicy       `json:"tool_policy"`
	}{
		params.Actor.Kind, params.Actor.ID, params.Actor.OrganizationID, params.AgentID,
		params.ExpectedRevision, params.Engine, params.ProviderProtocol, params.ProviderEndpoint,
		params.Model, params.CredentialBindingHandle, params.SandboxProvider,
		int64(params.MaxRunDuration), params.MaxOutputBytes, params.ToolPolicy,
	})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode runtime spec request payload: %w", err)
	}
	return sha256.Sum256(payload), nil
}
