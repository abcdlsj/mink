package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	artifactapp "github.com/abcdlsj/sumi/internal/artifact/application"
	"github.com/google/uuid"
)

type ArtifactGrant = artifactapp.Grant

type GrantArtifactParams = artifactapp.GrantCommand

type RevokeArtifactGrantParams = artifactapp.RevokeGrantCommand

type ArtifactSourceView = artifactapp.SourceView

type ArtifactExecutionView = artifactapp.ExecutionView

type ArtifactView = artifactapp.View

type ArtifactVersionView = artifactapp.VersionView

type GetArtifactParams = artifactapp.GetQuery

type ListArtifactsParams = artifactapp.ListQuery

type ListArtifactsResult = artifactapp.ListResult

type FetchArtifactParams = artifactapp.FetchQuery

type FetchArtifactResult = artifactapp.FetchResult

func (s *ArtifactStore) Grant(ctx context.Context, params GrantArtifactParams) (ArtifactGrant, error) {
	if err := validateGrantArtifactParams(params); err != nil {
		return ArtifactGrant{}, err
	}
	fingerprint, err := artifactFingerprint(struct {
		ArtifactID string `json:"artifact_id"`
		TargetKind string `json:"target_kind"`
		TargetID   string `json:"target_id"`
		Capability string `json:"capability"`
	}{params.ArtifactID, params.TargetKind, params.TargetID, params.Capability})
	if err != nil {
		return ArtifactGrant{}, err
	}
	tx, actor, _, err := s.beginTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ArtifactGrant{}, err
	}
	defer tx.Rollback()
	artifact, err := artifactByID(ctx, tx, params.ArtifactID, actor.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactGrant{}, ErrArtifactNotFound
	}
	if err != nil {
		return ArtifactGrant{}, err
	}
	allowed, err := artifactManageAllowed(ctx, tx, actor, artifact, params.Now)
	if err != nil {
		return ArtifactGrant{}, err
	}
	if !allowed {
		return ArtifactGrant{}, commitDenied(ctx, tx, actor, artifactAuditGrant, "artifact", artifact.ID, params.RequestID, "permission_missing", params.Now)
	}
	if receipt, found, err := readArtifactReceipt(ctx, tx, params.RequestID, actor, artifactAuditGrant, fingerprint); err != nil {
		return ArtifactGrant{}, err
	} else if found {
		return commitArtifactGrantReplay(ctx, tx, receipt)
	}
	if err := validateArtifactGrantTarget(ctx, tx, actor.OrganizationID, params.TargetKind, params.TargetID); err != nil {
		return ArtifactGrant{}, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM artifact_grants
			WHERE artifact_id = ? AND target_kind = ? AND target_id = ?
			  AND capability = ? AND revoked_at IS NULL
		)
	`, artifact.ID, params.TargetKind, params.TargetID, params.Capability).Scan(&exists); err != nil {
		return ArtifactGrant{}, fmt.Errorf("check active artifact grant: %w", err)
	}
	if exists {
		return ArtifactGrant{}, ErrArtifactInvalid
	}
	grant := ArtifactGrant{
		ID: uuid.NewString(), ArtifactID: artifact.ID, OrganizationID: artifact.OrganizationID,
		TargetKind: params.TargetKind, TargetID: params.TargetID, Capability: params.Capability,
		GrantedBy: actor, GrantedAt: params.Now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO artifact_grants(
			id, artifact_id, organization_id, target_kind, target_id, capability,
			granted_by_kind, granted_by_id, granted_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, grant.ID, grant.ArtifactID, grant.OrganizationID, grant.TargetKind, grant.TargetID,
		grant.Capability, actor.Kind, actor.ID, unixNano(params.Now)); err != nil {
		return ArtifactGrant{}, fmt.Errorf("persist artifact grant: %w", err)
	}
	if err := persistArtifactReceipt(ctx, tx, artifactReceipt{
		RequestID: params.RequestID, Actor: actor, Operation: artifactAuditGrant,
		Fingerprint: fingerprint, ResultKind: "grant", GrantID: grant.ID, CommittedAt: params.Now,
	}); err != nil {
		return ArtifactGrant{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: actor.OrganizationID, Actor: actor, Action: artifactAuditGrant,
		TargetKind: "artifact", TargetID: artifact.ID, RequestID: params.RequestID,
		Outcome: "committed", Now: params.Now,
	}); err != nil {
		return ArtifactGrant{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactGrant{}, fmt.Errorf("commit artifact grant: %w", err)
	}
	return grant, nil
}

func (s *ArtifactStore) RevokeGrant(ctx context.Context, params RevokeArtifactGrantParams) (ArtifactGrant, error) {
	if _, err := uuid.Parse(params.RequestID); err != nil || params.GrantID == "" || params.Now.IsZero() {
		return ArtifactGrant{}, ErrArtifactInvalid
	}
	fingerprint, err := artifactFingerprint(struct {
		GrantID string `json:"grant_id"`
	}{params.GrantID})
	if err != nil {
		return ArtifactGrant{}, err
	}
	tx, actor, _, err := s.beginTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ArtifactGrant{}, err
	}
	defer tx.Rollback()
	grant, err := artifactGrantByID(ctx, tx, params.GrantID, actor.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactGrant{}, ErrArtifactGrantNotFound
	}
	if err != nil {
		return ArtifactGrant{}, err
	}
	artifact, err := artifactByID(ctx, tx, grant.ArtifactID, actor.OrganizationID)
	if err != nil {
		return ArtifactGrant{}, ErrArtifactIntegrity
	}
	allowed, err := artifactManageAllowed(ctx, tx, actor, artifact, params.Now)
	if err != nil {
		return ArtifactGrant{}, err
	}
	if !allowed {
		return ArtifactGrant{}, commitDenied(ctx, tx, actor, artifactAuditRevoke, "artifact", artifact.ID, params.RequestID, "permission_missing", params.Now)
	}
	if receipt, found, err := readArtifactReceipt(ctx, tx, params.RequestID, actor, artifactAuditRevoke, fingerprint); err != nil {
		return ArtifactGrant{}, err
	} else if found {
		return commitArtifactGrantReplay(ctx, tx, receipt)
	}
	if grant.RevokedAt != nil {
		return ArtifactGrant{}, ErrArtifactInvalid
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE artifact_grants
		SET revoked_by_kind = ?, revoked_by_id = ?, revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL
	`, actor.Kind, actor.ID, unixNano(params.Now), grant.ID); err != nil {
		return ArtifactGrant{}, fmt.Errorf("revoke artifact grant: %w", err)
	}
	if err := persistArtifactReceipt(ctx, tx, artifactReceipt{
		RequestID: params.RequestID, Actor: actor, Operation: artifactAuditRevoke,
		Fingerprint: fingerprint, ResultKind: "grant", GrantID: grant.ID, CommittedAt: params.Now,
	}); err != nil {
		return ArtifactGrant{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: actor.OrganizationID, Actor: actor, Action: artifactAuditRevoke,
		TargetKind: "artifact", TargetID: artifact.ID, RequestID: params.RequestID,
		Outcome: "committed", Now: params.Now,
	}); err != nil {
		return ArtifactGrant{}, err
	}
	grant, err = artifactGrantByID(ctx, tx, grant.ID, actor.OrganizationID)
	if err != nil {
		return ArtifactGrant{}, ErrArtifactIntegrity
	}
	if err := tx.Commit(); err != nil {
		return ArtifactGrant{}, fmt.Errorf("commit artifact grant revoke: %w", err)
	}
	return grant, nil
}

func (s *ArtifactStore) Get(ctx context.Context, params GetArtifactParams) (ArtifactView, error) {
	if _, err := uuid.Parse(params.ArtifactID); err != nil || params.Now.IsZero() {
		return ArtifactView{}, ErrArtifactInvalid
	}
	tx, actor, authentication, err := s.beginTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ArtifactView{}, err
	}
	defer tx.Rollback()
	artifact, err := artifactByID(ctx, tx, params.ArtifactID, actor.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactView{}, ErrArtifactNotFound
	}
	if err != nil {
		return ArtifactView{}, err
	}
	allowed, err := artifactReadAllowed(ctx, tx, actor, artifact, params.Now)
	if err != nil {
		return ArtifactView{}, err
	}
	if !allowed {
		return ArtifactView{}, ErrPermissionDenied
	}
	versionNumber := params.Version
	if versionNumber == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT MAX(version) FROM artifact_versions WHERE artifact_id = ?`, artifact.ID).Scan(&versionNumber); err != nil {
			return ArtifactView{}, fmt.Errorf("read latest artifact version: %w", err)
		}
	}
	version, err := artifactVersionByID(ctx, tx, artifact.ID, versionNumber)
	if err != nil {
		return ArtifactView{}, err
	}
	view, err := projectArtifactView(ctx, tx, actor, authentication, artifact, version, params.Now)
	if err != nil {
		return ArtifactView{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactView{}, fmt.Errorf("commit artifact read: %w", err)
	}
	return view, nil
}

func (s *ArtifactStore) List(ctx context.Context, params ListArtifactsParams) (ListArtifactsResult, error) {
	if params.Now.IsZero() || params.Limit == 0 || params.Limit > 200 {
		return ListArtifactsResult{}, ErrArtifactInvalid
	}
	tx, actor, authentication, err := s.beginTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ListArtifactsResult{}, err
	}
	defer tx.Rollback()
	var afterCreatedAt time.Time
	afterID := params.AfterArtifactID
	if afterID != "" {
		cursor, err := artifactByID(ctx, tx, afterID, actor.OrganizationID)
		if errors.Is(err, sql.ErrNoRows) {
			return ListArtifactsResult{}, ErrArtifactCursorUnavailable
		}
		if err != nil {
			return ListArtifactsResult{}, err
		}
		allowed, err := artifactReadAllowed(ctx, tx, actor, cursor, params.Now)
		if err != nil {
			return ListArtifactsResult{}, err
		}
		if !allowed || params.OwningWorkID != "" && cursor.OwningWorkID != params.OwningWorkID {
			return ListArtifactsResult{}, ErrArtifactCursorUnavailable
		}
		afterCreatedAt = cursor.CreatedAt
	}
	batchSize := params.Limit + 1
	views := make([]ArtifactView, 0, batchSize)
	for len(views) < int(batchSize) {
		artifacts, err := listArtifactBatch(ctx, tx, actor.OrganizationID, params.OwningWorkID, afterCreatedAt, afterID, batchSize)
		if err != nil {
			return ListArtifactsResult{}, err
		}
		if len(artifacts) == 0 {
			break
		}
		for _, artifact := range artifacts {
			allowed, err := artifactReadAllowed(ctx, tx, actor, artifact, params.Now)
			if err != nil {
				return ListArtifactsResult{}, err
			}
			if !allowed {
				continue
			}
			var versionNumber uint64
			if err := tx.QueryRowContext(ctx, `SELECT MAX(version) FROM artifact_versions WHERE artifact_id = ?`, artifact.ID).Scan(&versionNumber); err != nil {
				return ListArtifactsResult{}, err
			}
			version, err := artifactVersionByID(ctx, tx, artifact.ID, versionNumber)
			if err != nil {
				return ListArtifactsResult{}, err
			}
			view, err := projectArtifactView(ctx, tx, actor, authentication, artifact, version, params.Now)
			if err != nil {
				return ListArtifactsResult{}, err
			}
			views = append(views, view)
			if len(views) == int(batchSize) {
				break
			}
		}
		last := artifacts[len(artifacts)-1]
		afterCreatedAt = last.CreatedAt
		afterID = last.ID
		if len(artifacts) < int(batchSize) {
			break
		}
	}
	if err := tx.Commit(); err != nil {
		return ListArtifactsResult{}, fmt.Errorf("commit artifact list: %w", err)
	}
	result := ListArtifactsResult{Views: views}
	if len(views) > int(params.Limit) {
		result.Views = views[:params.Limit]
		result.NextArtifactID = result.Views[len(result.Views)-1].ID
	}
	return result, nil
}

func listArtifactBatch(ctx context.Context, tx *sql.Tx, organizationID, owningWorkID string, afterCreatedAt time.Time, afterID string, limit uint32) ([]Artifact, error) {
	query := `
		SELECT id, organization_id, owning_work_id, name, media_type,
		       creator_kind, creator_id, created_at
		FROM artifacts
		WHERE organization_id = ?
	`
	arguments := []any{organizationID}
	if owningWorkID != "" {
		query += " AND owning_work_id = ?"
		arguments = append(arguments, owningWorkID)
	}
	if afterID != "" {
		query += " AND (created_at > ? OR (created_at = ? AND id > ?))"
		stamp := unixNano(afterCreatedAt)
		arguments = append(arguments, stamp, stamp, afterID)
	}
	query += " ORDER BY created_at, id LIMIT ?"
	arguments = append(arguments, limit)
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list artifact batch: %w", err)
	}
	defer rows.Close()
	var artifacts []Artifact
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact batch: %w", err)
	}
	return artifacts, nil
}

func (s *ArtifactStore) Fetch(ctx context.Context, params FetchArtifactParams) (FetchArtifactResult, error) {
	view, err := s.Get(ctx, GetArtifactParams(params))
	if err != nil {
		return FetchArtifactResult{}, err
	}
	if view.Version.IntegrityState != "ready" {
		return FetchArtifactResult{}, ErrArtifactIntegrity
	}
	content, err := s.blobs.Open(ctx, view.Version.Digest, view.Version.Size)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return FetchArtifactResult{}, err
		}
		return FetchArtifactResult{}, ErrArtifactIntegrity
	}
	return FetchArtifactResult{Artifact: view.Artifact, Version: view.Version, Content: content}, nil
}

func artifactManageAllowed(ctx context.Context, tx *sql.Tx, actor Principal, artifact Artifact, now time.Time) (bool, error) {
	reason, err := requireGrant(ctx, tx, actor, CapabilityWorkManage, Scope{Kind: "work", ID: artifact.OwningWorkID}, now, "")
	if err != nil || reason == "" {
		return reason == "", err
	}
	if actor.Kind != "agent" {
		return false, nil
	}
	return activeArtifactGrantExists(ctx, tx, artifact.ID, ArtifactGrantTargetAgent, actor.ID, ArtifactGrantManage)
}

func artifactReadAllowed(ctx context.Context, tx *sql.Tx, actor Principal, artifact Artifact, now time.Time) (bool, error) {
	reason, err := requireGrant(ctx, tx, actor, CapabilityWorkRead, Scope{Kind: "work", ID: artifact.OwningWorkID}, now, "")
	if err != nil || reason == "" {
		return reason == "", err
	}
	if actor.Kind == "agent" {
		for _, capability := range []string{ArtifactGrantRead, ArtifactGrantManage} {
			allowed, err := activeArtifactGrantExists(ctx, tx, artifact.ID, ArtifactGrantTargetAgent, actor.ID, capability)
			if err != nil || allowed {
				return allowed, err
			}
		}
	}
	targets, err := activeArtifactGrantTargets(ctx, tx, artifact.ID, ArtifactGrantTargetSpace, ArtifactGrantRead)
	if err != nil {
		return false, err
	}
	for _, spaceID := range targets {
		member, err := activeMembershipExists(ctx, tx, spaceID, actor)
		if err != nil {
			return false, err
		}
		if !member {
			continue
		}
		reason, err := requireGrant(ctx, tx, actor, CapabilitySpaceRead, Scope{Kind: "space", ID: spaceID}, now, "")
		if err != nil || reason == "" {
			return reason == "", err
		}
	}
	targets, err = activeArtifactGrantTargets(ctx, tx, artifact.ID, ArtifactGrantTargetWork, ArtifactGrantRead)
	if err != nil {
		return false, err
	}
	for _, workID := range targets {
		reason, err := requireGrant(ctx, tx, actor, CapabilityWorkRead, Scope{Kind: "work", ID: workID}, now, "")
		if err != nil || reason == "" {
			return reason == "", err
		}
	}
	return false, nil
}

func projectArtifactView(ctx context.Context, tx *sql.Tx, actor Principal, authentication AgentRuntimeAuthentication, artifact Artifact, version ArtifactVersion, now time.Time) (ArtifactView, error) {
	view := ArtifactView{Artifact: artifact, Version: ArtifactVersionView{
		ArtifactID: version.ArtifactID, OrganizationID: version.OrganizationID,
		Version: version.Version, Digest: version.Digest, Size: version.Size,
		IntegrityState: version.IntegrityState, Summary: version.Summary,
		Author: version.Author, CreatedAt: version.CreatedAt,
	}}
	reason, err := requireGrant(ctx, tx, actor, CapabilityWorkRead, Scope{Kind: "work", ID: artifact.OwningWorkID}, now, "")
	if err != nil {
		return ArtifactView{}, err
	}
	if reason != "" {
		view.OwningWorkID = ""
		view.OwningWorkRestricted = true
	}
	if version.Execution != nil {
		visible, err := artifactExecutionReadable(ctx, tx, actor, authentication, *version.Execution, now)
		if err != nil {
			return ArtifactView{}, err
		}
		if visible {
			view.Version.Execution = &ArtifactExecutionView{
				DeliveryID: version.Execution.DeliveryID, RunID: version.Execution.RunID,
				LaunchID: version.Execution.LaunchID, AgentID: version.Execution.AgentID,
				ComputerID:          version.Execution.ComputerID,
				PlacementGeneration: version.Execution.PlacementGeneration, Fence: version.Execution.Fence,
			}
		} else {
			view.Version.Execution = &ArtifactExecutionView{Restricted: true}
		}
	}
	for _, source := range version.Sources {
		visible := false
		switch source.Kind {
		case ArtifactSourceMessage:
			visible, err = messageSourceReadable(ctx, tx, actor, source.MessageID, now)
		case ArtifactSourceVersion:
			var upstream Artifact
			upstream, err = artifactByID(ctx, tx, source.ArtifactID, actor.OrganizationID)
			if err == nil {
				visible, err = artifactReadAllowed(ctx, tx, actor, upstream, now)
			}
		}
		if err != nil {
			return ArtifactView{}, err
		}
		if !visible {
			view.Version.Sources = append(view.Version.Sources, ArtifactSourceView{Restricted: true})
			continue
		}
		view.Version.Sources = append(view.Version.Sources, ArtifactSourceView{
			Kind: source.Kind, MessageID: source.MessageID,
			ArtifactID: source.ArtifactID, ArtifactVersion: source.ArtifactVersion,
		})
	}
	return view, nil
}

func artifactExecutionReadable(ctx context.Context, tx *sql.Tx, actor Principal, authentication AgentRuntimeAuthentication, execution ArtifactExecution, now time.Time) (bool, error) {
	if actor.Kind == "agent" {
		if actor.ID != execution.AgentID || !authentication.Valid() {
			return false, nil
		}
		reason, err := requireGrant(ctx, tx, actor, CapabilityRunExecute, Scope{Kind: "agent", ID: actor.ID}, now, "")
		if err != nil || reason != "" {
			return false, err
		}
		run, err := requireOwnedRun(ctx, tx, actor.ID, execution.RunID)
		if errors.Is(err, ErrRunNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if run.DeliveryID != execution.DeliveryID || run.State != RunStateRunning {
			return false, nil
		}
		delivery, _, _, err := requireOwnedDelivery(ctx, tx, actor.ID, execution.DeliveryID)
		if errors.Is(err, ErrDeliveryNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if delivery.State != DeliveryStateAccepted {
			return false, nil
		}
		launch, found, err := currentRunLaunch(ctx, tx, run.ID)
		if err != nil {
			return false, err
		}
		if !found || launch.ID != execution.LaunchID || launch.Fence != execution.Fence ||
			launch.HolderComputerID != execution.ComputerID ||
			launch.HolderPlacementGeneration != execution.PlacementGeneration ||
			!runLaunchHeldBy(launch, authentication.Proof) || !launch.ExpiresAt.After(now) {
			return false, nil
		}
		return true, nil
	}
	reason, err := requireGrant(ctx, tx, actor, CapabilityAuditRead, Scope{Kind: "organization", ID: actor.OrganizationID}, now, "")
	if err != nil {
		return false, err
	}
	return reason == "", nil
}

func messageSourceReadable(ctx context.Context, tx *sql.Tx, actor Principal, messageID string, now time.Time) (bool, error) {
	var spaceID, organizationID string
	err := tx.QueryRowContext(ctx, `
		SELECT m.space_id, s.organization_id
		FROM messages m JOIN spaces s ON s.id = m.space_id
		WHERE m.id = ?
	`, messageID).Scan(&spaceID, &organizationID)
	if errors.Is(err, sql.ErrNoRows) || organizationID != actor.OrganizationID {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	reason, err := requireGrant(ctx, tx, actor, CapabilitySpaceRead, Scope{Kind: "space", ID: spaceID}, now, "")
	if err != nil || reason != "" {
		return false, err
	}
	return activeMembershipExists(ctx, tx, spaceID, actor)
}

func activeMembershipExists(ctx context.Context, tx *sql.Tx, spaceID string, actor Principal) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM space_memberships
			WHERE space_id = ? AND principal_kind = ? AND principal_id = ?
		)
	`, spaceID, actor.Kind, actor.ID).Scan(&exists)
	return exists, err
}

func activeArtifactGrantExists(ctx context.Context, tx *sql.Tx, artifactID, targetKind, targetID, capability string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM artifact_grants
			WHERE artifact_id = ? AND target_kind = ? AND target_id = ?
			  AND capability = ? AND revoked_at IS NULL
		)
	`, artifactID, targetKind, targetID, capability).Scan(&exists)
	return exists, err
}

func activeArtifactGrantTargets(ctx context.Context, tx *sql.Tx, artifactID, targetKind, capability string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT target_id FROM artifact_grants
		WHERE artifact_id = ? AND target_kind = ? AND capability = ? AND revoked_at IS NULL
		ORDER BY granted_at, id
	`, artifactID, targetKind, capability)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func validateArtifactGrantTarget(ctx context.Context, tx *sql.Tx, organizationID, kind, id string) error {
	var exists bool
	var err error
	switch kind {
	case ArtifactGrantTargetAgent:
		exists, err = agentExists(ctx, tx, id)
	case ArtifactGrantTargetSpace:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM spaces WHERE id = ? AND organization_id = ?)`, id, organizationID).Scan(&exists)
	case ArtifactGrantTargetWork:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM works WHERE id = ? AND organization_id = ?)`, id, organizationID).Scan(&exists)
	default:
		return ErrArtifactInvalid
	}
	if err != nil {
		return err
	}
	if !exists {
		return ErrArtifactInvalid
	}
	return nil
}

func validateGrantArtifactParams(params GrantArtifactParams) error {
	if _, err := uuid.Parse(params.RequestID); err != nil {
		return ErrArtifactInvalid
	}
	if _, err := uuid.Parse(params.ArtifactID); err != nil {
		return ErrArtifactInvalid
	}
	if _, err := uuid.Parse(params.TargetID); err != nil || params.Now.IsZero() {
		return ErrArtifactInvalid
	}
	if params.TargetKind != ArtifactGrantTargetAgent && params.TargetKind != ArtifactGrantTargetSpace && params.TargetKind != ArtifactGrantTargetWork {
		return ErrArtifactInvalid
	}
	if params.Capability != ArtifactGrantRead && params.Capability != ArtifactGrantManage {
		return ErrArtifactInvalid
	}
	if params.Capability == ArtifactGrantManage && params.TargetKind != ArtifactGrantTargetAgent {
		return ErrArtifactInvalid
	}
	return nil
}

func artifactGrantByID(ctx context.Context, tx *sql.Tx, grantID, organizationID string) (ArtifactGrant, error) {
	var grant ArtifactGrant
	var granted int64
	var revoked sql.NullInt64
	var revokedKind, revokedID sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT id, artifact_id, organization_id, target_kind, target_id, capability,
		       granted_by_kind, granted_by_id, granted_at,
		       revoked_by_kind, revoked_by_id, revoked_at
		FROM artifact_grants WHERE id = ? AND organization_id = ?
	`, grantID, organizationID).Scan(&grant.ID, &grant.ArtifactID, &grant.OrganizationID,
		&grant.TargetKind, &grant.TargetID, &grant.Capability, &grant.GrantedBy.Kind,
		&grant.GrantedBy.ID, &granted, &revokedKind, &revokedID, &revoked)
	if err != nil {
		return ArtifactGrant{}, err
	}
	grant.GrantedBy.OrganizationID = organizationID
	grant.GrantedAt = timeFromUnixNano(granted)
	if revoked.Valid {
		stamp := timeFromUnixNano(revoked.Int64)
		principal := Principal{Kind: PrincipalKind(revokedKind.String), ID: revokedID.String, OrganizationID: organizationID}
		grant.RevokedAt = &stamp
		grant.RevokedBy = &principal
	}
	return grant, nil
}

func commitArtifactGrantReplay(ctx context.Context, tx *sql.Tx, receipt artifactReceipt) (ArtifactGrant, error) {
	grant, err := artifactGrantByID(ctx, tx, receipt.GrantID, receipt.Actor.OrganizationID)
	if err != nil {
		return ArtifactGrant{}, ErrArtifactIntegrity
	}
	if err := tx.Commit(); err != nil {
		return ArtifactGrant{}, fmt.Errorf("commit artifact grant replay: %w", err)
	}
	return grant, nil
}
