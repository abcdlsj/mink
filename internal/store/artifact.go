package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	ArtifactMaxBlobSize  int64 = 64 << 20
	ArtifactMaxChunkSize       = 64 << 10

	ArtifactGrantRead   = "read"
	ArtifactGrantManage = "manage"

	ArtifactGrantTargetAgent = "agent"
	ArtifactGrantTargetSpace = "space"
	ArtifactGrantTargetWork  = "work"

	ArtifactSourceMessage = "message"
	ArtifactSourceVersion = "artifact_version"

	artifactAuditPublish = "artifact.publish"
	artifactAuditGrant   = "artifact.grant"
	artifactAuditRevoke  = "artifact.revoke"
)

type ArtifactBlobStore interface {
	Put(context.Context, io.Reader, int64) ([sha256.Size]byte, int64, error)
	Open(context.Context, [sha256.Size]byte, int64) (io.ReadCloser, error)
	Reconcile(context.Context, map[[sha256.Size]byte]int64, time.Time, time.Duration) (map[[sha256.Size]byte]string, int, int, error)
}

type ArtifactStore struct {
	database            *Store
	blobs               ArtifactBlobStore
	maxBlobSize         int64
	quarantineRetention time.Duration
}

func NewArtifactStore(database *Store, blobs ArtifactBlobStore, maxBlobSize int64) (*ArtifactStore, error) {
	if database == nil || blobs == nil || maxBlobSize <= 0 || maxBlobSize > ArtifactMaxBlobSize {
		return nil, ErrArtifactInvalid
	}
	return &ArtifactStore{
		database: database, blobs: blobs, maxBlobSize: maxBlobSize,
		quarantineRetention: 24 * time.Hour,
	}, nil
}

type ArtifactAuthentication struct {
	Human Principal
	Agent AgentRuntimeAuthentication
}

type Artifact struct {
	ID             string
	OrganizationID string
	OwningWorkID   string
	Name           string
	MediaType      string
	Creator        Principal
	CreatedAt      time.Time
}

type ArtifactVersion struct {
	ArtifactID     string
	OrganizationID string
	Version        uint64
	Digest         [sha256.Size]byte
	Size           int64
	IntegrityState string
	Summary        string
	Author         Principal
	CreatedAt      time.Time
	Execution      *ArtifactExecution
	Sources        []ArtifactSource
}

type ArtifactExecution struct {
	DeliveryID          string
	RunID               string
	LaunchID            string
	AgentID             string
	ComputerID          string
	PlacementGeneration uint64
	Fence               uint64
}

type ArtifactExecutionInput struct {
	DeliveryID string
	RunID      string
	LaunchID   string
	Fence      uint64
}

type ArtifactSourceInput struct {
	Kind            string
	MessageID       string
	ArtifactID      string
	ArtifactVersion uint64
}

type ArtifactSource struct {
	Kind            string
	MessageID       string
	ArtifactID      string
	ArtifactVersion uint64
}

type PublishArtifactParams struct {
	RequestID      string
	Authentication ArtifactAuthentication
	ArtifactID     string
	OwningWorkID   string
	Name           string
	MediaType      string
	Summary        string
	Execution      *ArtifactExecutionInput
	Sources        []ArtifactSourceInput
	Content        io.Reader
	Now            time.Time
}

type PublishArtifactResult struct {
	Artifact    Artifact
	Version     ArtifactVersion
	CommittedAt time.Time
}

type artifactReceipt struct {
	RequestID   string
	Actor       Principal
	Operation   string
	Fingerprint [sha256.Size]byte
	ResultKind  string
	ArtifactID  string
	Version     uint64
	GrantID     string
	CommittedAt time.Time
}

func (s *ArtifactStore) Publish(ctx context.Context, params PublishArtifactParams) (PublishArtifactResult, error) {
	if err := validatePublishArtifactParams(params); err != nil {
		return PublishArtifactResult{}, err
	}
	initial, actor, initialAuthentication, err := s.beginTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return PublishArtifactResult{}, err
	}
	if err := s.authorizePublish(ctx, initial, actor, params, params.Now); err != nil {
		initial.Rollback()
		return PublishArtifactResult{}, err
	}
	if _, err := s.validateExecution(ctx, initial, actor, initialAuthentication, params.Execution, params.Now); err != nil {
		initial.Rollback()
		return PublishArtifactResult{}, err
	}
	if err := s.validateSources(ctx, initial, actor, params.Sources, params.Now); err != nil {
		initial.Rollback()
		return PublishArtifactResult{}, err
	}
	if err := initial.Commit(); err != nil {
		return PublishArtifactResult{}, fmt.Errorf("commit initial artifact authorization: %w", err)
	}

	digest, size, err := s.blobs.Put(ctx, params.Content, s.maxBlobSize)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return PublishArtifactResult{}, err
		}
		return PublishArtifactResult{}, ErrArtifactBlobUnavailable
	}
	fingerprint, err := artifactFingerprint(struct {
		ArtifactID   string                  `json:"artifact_id"`
		OwningWorkID string                  `json:"owning_work_id"`
		Name         string                  `json:"name"`
		MediaType    string                  `json:"media_type"`
		Summary      string                  `json:"summary"`
		Execution    *ArtifactExecutionInput `json:"execution,omitempty"`
		Sources      []ArtifactSourceInput   `json:"sources,omitempty"`
		Digest       [sha256.Size]byte       `json:"digest"`
		Size         int64                   `json:"size"`
	}{params.ArtifactID, params.OwningWorkID, params.Name, params.MediaType, params.Summary, params.Execution, params.Sources, digest, size})
	if err != nil {
		return PublishArtifactResult{}, err
	}

	tx, actor, authentication, err := s.beginTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return PublishArtifactResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorizePublish(ctx, tx, actor, params, params.Now); err != nil {
		return PublishArtifactResult{}, err
	}
	if receipt, found, err := readArtifactReceipt(ctx, tx, params.RequestID, actor, artifactAuditPublish, fingerprint); err != nil {
		return PublishArtifactResult{}, err
	} else if found {
		return commitArtifactPublishReplay(ctx, tx, receipt)
	}

	artifact, creating, err := loadPublishArtifact(ctx, tx, actor.OrganizationID, params)
	if err != nil {
		return PublishArtifactResult{}, err
	}
	if creating {
		artifact = Artifact{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID,
			OwningWorkID: params.OwningWorkID, Name: params.Name, MediaType: params.MediaType,
			Creator: actor, CreatedAt: params.Now,
		}
		if err := insertArtifact(ctx, tx, artifact); err != nil {
			return PublishArtifactResult{}, err
		}
	}
	versionNumber, err := nextArtifactVersion(ctx, tx, artifact.ID)
	if err != nil {
		return PublishArtifactResult{}, err
	}
	execution, err := s.validateExecution(ctx, tx, actor, authentication, params.Execution, params.Now)
	if err != nil {
		return PublishArtifactResult{}, err
	}
	if err := s.validateSources(ctx, tx, actor, params.Sources, params.Now); err != nil {
		return PublishArtifactResult{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO artifact_blobs(digest, size, integrity_state, created_at, checked_at)
		VALUES(?, ?, 'ready', ?, ?)
		ON CONFLICT(digest) DO UPDATE SET
			integrity_state = 'ready',
			checked_at = MAX(artifact_blobs.checked_at, excluded.checked_at)
		WHERE artifact_blobs.size = excluded.size
	`, digest[:], size, unixNano(params.Now), unixNano(params.Now))
	if err != nil {
		return PublishArtifactResult{}, fmt.Errorf("persist artifact blob metadata: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return PublishArtifactResult{}, fmt.Errorf("inspect artifact blob metadata write: %w", err)
	}
	if changed != 1 {
		return PublishArtifactResult{}, ErrArtifactIntegrity
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO artifact_versions(
			artifact_id, organization_id, version, digest, size, summary,
			author_kind, author_id, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, artifact.ID, artifact.OrganizationID, versionNumber, digest[:], size, params.Summary,
		actor.Kind, actor.ID, unixNano(params.Now)); err != nil {
		return PublishArtifactResult{}, fmt.Errorf("persist artifact version: %w", err)
	}
	if execution != nil {
		if err := insertArtifactExecution(ctx, tx, artifact, versionNumber, *execution); err != nil {
			return PublishArtifactResult{}, err
		}
	}
	if err := insertArtifactSources(ctx, tx, artifact, versionNumber, params.Sources); err != nil {
		return PublishArtifactResult{}, err
	}
	if err := persistArtifactReceipt(ctx, tx, artifactReceipt{
		RequestID: params.RequestID, Actor: actor, Operation: artifactAuditPublish, Fingerprint: fingerprint,
		ResultKind: "version", ArtifactID: artifact.ID, Version: versionNumber, CommittedAt: params.Now,
	}); err != nil {
		return PublishArtifactResult{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: actor.OrganizationID, Actor: actor, Action: artifactAuditPublish,
		TargetKind: "artifact", TargetID: artifact.ID, RequestID: params.RequestID,
		Outcome: "committed", Now: params.Now,
	}); err != nil {
		return PublishArtifactResult{}, err
	}
	version, err := artifactVersionByID(ctx, tx, artifact.ID, versionNumber)
	if err != nil {
		return PublishArtifactResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishArtifactResult{}, fmt.Errorf("commit artifact publish: %w", err)
	}
	return PublishArtifactResult{Artifact: artifact, Version: version, CommittedAt: params.Now}, nil
}

func (s *ArtifactStore) beginTransaction(ctx context.Context, authentication ArtifactAuthentication, now time.Time) (*sql.Tx, Principal, AgentRuntimeAuthentication, error) {
	if now.IsZero() {
		return nil, Principal{}, AgentRuntimeAuthentication{}, ErrArtifactInvalid
	}
	tx, err := s.database.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, Principal{}, AgentRuntimeAuthentication{}, fmt.Errorf("begin artifact transaction: %w", err)
	}
	if authentication.Agent.Valid() {
		if authentication.Human.ID != "" {
			tx.Rollback()
			return nil, Principal{}, AgentRuntimeAuthentication{}, ErrArtifactInvalid
		}
		current, err := requireAgentRuntimeSession(ctx, tx, authentication.Agent.Proof, now)
		if err != nil {
			tx.Rollback()
			return nil, Principal{}, AgentRuntimeAuthentication{}, err
		}
		return tx, current.Principal, current, nil
	}
	actor := authentication.Human
	if actor.Kind != "human" || actor.ID == "" || actor.OrganizationID == "" {
		tx.Rollback()
		return nil, Principal{}, AgentRuntimeAuthentication{}, ErrArtifactInvalid
	}
	active, err := principalActive(ctx, tx, actor)
	if err != nil {
		tx.Rollback()
		return nil, Principal{}, AgentRuntimeAuthentication{}, err
	}
	if !active {
		tx.Rollback()
		return nil, Principal{}, AgentRuntimeAuthentication{}, ErrPermissionDenied
	}
	return tx, actor, AgentRuntimeAuthentication{}, nil
}

func (s *ArtifactStore) authorizePublish(ctx context.Context, tx *sql.Tx, actor Principal, params PublishArtifactParams, now time.Time) error {
	if params.ArtifactID == "" {
		var workExists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM works WHERE id = ? AND organization_id = ?)
		`, params.OwningWorkID, actor.OrganizationID).Scan(&workExists); err != nil {
			return fmt.Errorf("verify artifact owning work: %w", err)
		}
		if !workExists {
			return ErrArtifactInvalid
		}
		reason, err := requireGrant(ctx, tx, actor, CapabilityWorkManage, Scope{Kind: "work", ID: params.OwningWorkID}, now, "")
		if err != nil {
			return err
		}
		if reason != "" {
			return commitDenied(ctx, tx, actor, artifactAuditPublish, "artifact", "", params.RequestID, reason, now)
		}
		return nil
	}
	artifact, err := artifactByID(ctx, tx, params.ArtifactID, actor.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrArtifactNotFound
	}
	if err != nil {
		return err
	}
	if artifact.OwningWorkID != params.OwningWorkID || artifact.Name != params.Name || artifact.MediaType != params.MediaType {
		return ErrArtifactInvalid
	}
	allowed, err := artifactManageAllowed(ctx, tx, actor, artifact, now)
	if err != nil {
		return err
	}
	if !allowed {
		return commitDenied(ctx, tx, actor, artifactAuditPublish, "artifact", artifact.ID, params.RequestID, "permission_missing", now)
	}
	return nil
}

func (s *ArtifactStore) validateExecution(ctx context.Context, tx *sql.Tx, actor Principal, authentication AgentRuntimeAuthentication, input *ArtifactExecutionInput, now time.Time) (*ArtifactExecution, error) {
	if actor.Kind == "human" {
		if input != nil {
			return nil, ErrArtifactInvalid
		}
		return nil, nil
	}
	if actor.Kind != "agent" || input == nil || input.DeliveryID == "" || input.RunID == "" || input.LaunchID == "" || input.Fence == 0 || !authentication.Valid() {
		return nil, ErrArtifactInvalid
	}
	run, err := requireOwnedRun(ctx, tx, actor.ID, input.RunID)
	if err != nil {
		return nil, err
	}
	if run.DeliveryID != input.DeliveryID || run.State != RunStateRunning {
		return nil, ErrRunNotRunning
	}
	delivery, _, _, err := requireOwnedDelivery(ctx, tx, actor.ID, input.DeliveryID)
	if err != nil {
		return nil, err
	}
	if delivery.State != DeliveryStateAccepted {
		return nil, ErrRunNotRunning
	}
	launch, found, err := currentRunLaunch(ctx, tx, run.ID)
	if err != nil {
		return nil, err
	}
	if !found || launch.ID != input.LaunchID || launch.Fence != input.Fence || !runLaunchHeldBy(launch, authentication.Proof) {
		return nil, ErrRunLaunchStale
	}
	if !launch.ExpiresAt.After(now) {
		return nil, ErrRunLaunchExpired
	}
	return &ArtifactExecution{
		DeliveryID: delivery.ID, RunID: run.ID, LaunchID: launch.ID, AgentID: actor.ID,
		ComputerID: launch.HolderComputerID, PlacementGeneration: launch.HolderPlacementGeneration,
		Fence: launch.Fence,
	}, nil
}

func (s *ArtifactStore) validateSources(ctx context.Context, tx *sql.Tx, actor Principal, sources []ArtifactSourceInput, now time.Time) error {
	for _, source := range sources {
		switch source.Kind {
		case ArtifactSourceMessage:
			allowed, err := messageSourceReadable(ctx, tx, actor, source.MessageID, now)
			if err != nil {
				return err
			}
			if !allowed {
				return ErrPermissionDenied
			}
		case ArtifactSourceVersion:
			artifact, err := artifactByID(ctx, tx, source.ArtifactID, actor.OrganizationID)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrArtifactVersionNotFound
			}
			if err != nil {
				return err
			}
			if _, err := artifactVersionByID(ctx, tx, source.ArtifactID, source.ArtifactVersion); err != nil {
				return err
			}
			allowed, err := artifactReadAllowed(ctx, tx, actor, artifact, now)
			if err != nil {
				return err
			}
			if !allowed {
				return ErrPermissionDenied
			}
		default:
			return ErrArtifactInvalid
		}
	}
	return nil
}

func validatePublishArtifactParams(params PublishArtifactParams) error {
	if _, err := uuid.Parse(params.RequestID); err != nil || params.Content == nil || params.OwningWorkID == "" || params.Now.IsZero() {
		return ErrArtifactInvalid
	}
	if params.ArtifactID != "" {
		if _, err := uuid.Parse(params.ArtifactID); err != nil {
			return ErrArtifactInvalid
		}
	}
	if !validArtifactText(params.Name, 255) || !validArtifactText(params.MediaType, 255) || !validArtifactText(params.Summary, 20000) || len(params.Sources) > 100 {
		return ErrArtifactInvalid
	}
	seen := make(map[string]struct{}, len(params.Sources))
	for _, source := range params.Sources {
		var key string
		switch source.Kind {
		case ArtifactSourceMessage:
			if _, err := uuid.Parse(source.MessageID); err != nil || source.ArtifactID != "" || source.ArtifactVersion != 0 {
				return ErrArtifactInvalid
			}
			key = source.Kind + ":" + source.MessageID
		case ArtifactSourceVersion:
			if _, err := uuid.Parse(source.ArtifactID); err != nil || source.ArtifactVersion == 0 || source.MessageID != "" {
				return ErrArtifactInvalid
			}
			key = fmt.Sprintf("%s:%s:%d", source.Kind, source.ArtifactID, source.ArtifactVersion)
		default:
			return ErrArtifactInvalid
		}
		if _, duplicate := seen[key]; duplicate {
			return ErrArtifactInvalid
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validArtifactText(value string, max int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= max
}

func artifactFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode artifact request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func loadPublishArtifact(ctx context.Context, tx *sql.Tx, organizationID string, params PublishArtifactParams) (Artifact, bool, error) {
	if params.ArtifactID == "" {
		return Artifact{}, true, nil
	}
	artifact, err := artifactByID(ctx, tx, params.ArtifactID, organizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, false, ErrArtifactNotFound
	}
	if err != nil {
		return Artifact{}, false, err
	}
	return artifact, false, nil
}

func insertArtifact(ctx context.Context, tx *sql.Tx, artifact Artifact) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO artifacts(
			id, organization_id, owning_work_id, name, media_type,
			creator_kind, creator_id, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, artifact.ID, artifact.OrganizationID, artifact.OwningWorkID, artifact.Name, artifact.MediaType,
		artifact.Creator.Kind, artifact.Creator.ID, unixNano(artifact.CreatedAt))
	if err != nil {
		return fmt.Errorf("persist artifact: %w", err)
	}
	return nil
}

func insertArtifactExecution(ctx context.Context, tx *sql.Tx, artifact Artifact, version uint64, execution ArtifactExecution) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO artifact_version_executions(
			artifact_id, version, organization_id, delivery_id, run_id, launch_id,
			agent_id, computer_id, placement_generation, fence
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, artifact.ID, version, artifact.OrganizationID, execution.DeliveryID, execution.RunID,
		execution.LaunchID, execution.AgentID, execution.ComputerID,
		execution.PlacementGeneration, execution.Fence)
	if err != nil {
		return fmt.Errorf("persist artifact execution provenance: %w", err)
	}
	return nil
}

func insertArtifactSources(ctx context.Context, tx *sql.Tx, artifact Artifact, version uint64, sources []ArtifactSourceInput) error {
	for ordinal, source := range sources {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO artifact_version_sources(
				id, artifact_id, version, organization_id, ordinal, source_kind,
				source_message_id, source_artifact_id, source_artifact_version
			) VALUES(?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, 0))
		`, uuid.NewString(), artifact.ID, version, artifact.OrganizationID, ordinal, source.Kind,
			source.MessageID, source.ArtifactID, source.ArtifactVersion)
		if err != nil {
			return fmt.Errorf("persist artifact source: %w", err)
		}
	}
	return nil
}

func nextArtifactVersion(ctx context.Context, tx *sql.Tx, artifactID string) (uint64, error) {
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM artifact_versions WHERE artifact_id = ?`, artifactID).Scan(&version); err != nil {
		return 0, fmt.Errorf("allocate artifact version: %w", err)
	}
	return version, nil
}

func artifactByID(ctx context.Context, tx *sql.Tx, artifactID, organizationID string) (Artifact, error) {
	return scanArtifact(tx.QueryRowContext(ctx, `
		SELECT id, organization_id, owning_work_id, name, media_type,
		       creator_kind, creator_id, created_at
		FROM artifacts WHERE id = ? AND organization_id = ?
	`, artifactID, organizationID))
}

func scanArtifact(row scanner) (Artifact, error) {
	var artifact Artifact
	var created int64
	if err := row.Scan(&artifact.ID, &artifact.OrganizationID, &artifact.OwningWorkID,
		&artifact.Name, &artifact.MediaType, &artifact.Creator.Kind, &artifact.Creator.ID, &created); err != nil {
		return Artifact{}, err
	}
	artifact.Creator.OrganizationID = artifact.OrganizationID
	artifact.CreatedAt = timeFromUnixNano(created)
	return artifact, nil
}

func artifactVersionByID(ctx context.Context, tx *sql.Tx, artifactID string, version uint64) (ArtifactVersion, error) {
	var value ArtifactVersion
	var digest []byte
	var created int64
	err := tx.QueryRowContext(ctx, `
		SELECT v.artifact_id, v.organization_id, v.version, v.digest, v.size,
		       b.integrity_state, v.summary, v.author_kind, v.author_id, v.created_at
		FROM artifact_versions v
		JOIN artifact_blobs b ON b.digest = v.digest
		WHERE v.artifact_id = ? AND v.version = ?
	`, artifactID, version).Scan(&value.ArtifactID, &value.OrganizationID, &value.Version,
		&digest, &value.Size, &value.IntegrityState, &value.Summary,
		&value.Author.Kind, &value.Author.ID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactVersion{}, ErrArtifactVersionNotFound
	}
	if err != nil {
		return ArtifactVersion{}, fmt.Errorf("read artifact version: %w", err)
	}
	if len(digest) != sha256.Size {
		return ArtifactVersion{}, ErrArtifactIntegrity
	}
	copy(value.Digest[:], digest)
	value.Author.OrganizationID = value.OrganizationID
	value.CreatedAt = timeFromUnixNano(created)
	if err := loadArtifactVersionParts(ctx, tx, &value); err != nil {
		return ArtifactVersion{}, err
	}
	return value, nil
}

func loadArtifactVersionParts(ctx context.Context, tx *sql.Tx, version *ArtifactVersion) error {
	var execution ArtifactExecution
	err := tx.QueryRowContext(ctx, `
		SELECT delivery_id, run_id, launch_id, agent_id, computer_id, placement_generation, fence
		FROM artifact_version_executions WHERE artifact_id = ? AND version = ?
	`, version.ArtifactID, version.Version).Scan(&execution.DeliveryID, &execution.RunID, &execution.LaunchID,
		&execution.AgentID, &execution.ComputerID, &execution.PlacementGeneration, &execution.Fence)
	if err == nil {
		version.Execution = &execution
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read artifact execution provenance: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT source_kind, COALESCE(source_message_id, ''), COALESCE(source_artifact_id, ''),
		       COALESCE(source_artifact_version, 0)
		FROM artifact_version_sources
		WHERE artifact_id = ? AND version = ? ORDER BY ordinal
	`, version.ArtifactID, version.Version)
	if err != nil {
		return fmt.Errorf("read artifact sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var source ArtifactSource
		if err := rows.Scan(&source.Kind, &source.MessageID, &source.ArtifactID, &source.ArtifactVersion); err != nil {
			return err
		}
		version.Sources = append(version.Sources, source)
	}
	return rows.Err()
}

func readArtifactReceipt(ctx context.Context, tx *sql.Tx, requestID string, actor Principal, operation string, fingerprint [sha256.Size]byte) (artifactReceipt, bool, error) {
	var receipt artifactReceipt
	var stored []byte
	var version sql.NullInt64
	var committed int64
	err := tx.QueryRowContext(ctx, `
		SELECT actor_kind, actor_id, operation, payload_fingerprint, result_kind,
		       COALESCE(result_artifact_id, ''), result_version, COALESCE(result_grant_id, ''), committed_at
		FROM artifact_requests WHERE request_id = ?
	`, requestID).Scan(&receipt.Actor.Kind, &receipt.Actor.ID, &receipt.Operation, &stored,
		&receipt.ResultKind, &receipt.ArtifactID, &version, &receipt.GrantID, &committed)
	if errors.Is(err, sql.ErrNoRows) {
		return artifactReceipt{}, false, nil
	}
	if err != nil {
		return artifactReceipt{}, false, fmt.Errorf("read artifact request receipt: %w", err)
	}
	if receipt.Actor.Kind != actor.Kind || receipt.Actor.ID != actor.ID || receipt.Operation != operation || !bytes.Equal(stored, fingerprint[:]) {
		return artifactReceipt{}, false, ErrArtifactRequestConflict
	}
	receipt.Actor.OrganizationID = actor.OrganizationID
	receipt.RequestID = requestID
	copy(receipt.Fingerprint[:], stored)
	if version.Valid {
		receipt.Version = uint64(version.Int64)
	}
	receipt.CommittedAt = timeFromUnixNano(committed)
	return receipt, true, nil
}

func persistArtifactReceipt(ctx context.Context, tx *sql.Tx, receipt artifactReceipt) error {
	var artifactID any
	var version any
	var grantID any
	if receipt.ResultKind == "version" {
		artifactID, version = receipt.ArtifactID, receipt.Version
	} else {
		grantID = receipt.GrantID
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO artifact_requests(
			request_id, actor_kind, actor_id, operation, payload_fingerprint, result_kind,
			result_artifact_id, result_version, result_grant_id, committed_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, receipt.RequestID, receipt.Actor.Kind, receipt.Actor.ID, receipt.Operation,
		receipt.Fingerprint[:], receipt.ResultKind, artifactID, version, grantID, unixNano(receipt.CommittedAt))
	if err != nil {
		return fmt.Errorf("persist artifact request receipt: %w", err)
	}
	return nil
}

func commitArtifactPublishReplay(ctx context.Context, tx *sql.Tx, receipt artifactReceipt) (PublishArtifactResult, error) {
	artifact, err := artifactByID(ctx, tx, receipt.ArtifactID, receipt.Actor.OrganizationID)
	if err != nil {
		return PublishArtifactResult{}, ErrArtifactIntegrity
	}
	version, err := artifactVersionByID(ctx, tx, receipt.ArtifactID, receipt.Version)
	if err != nil {
		return PublishArtifactResult{}, ErrArtifactIntegrity
	}
	if err := tx.Commit(); err != nil {
		return PublishArtifactResult{}, fmt.Errorf("commit artifact publish replay: %w", err)
	}
	return PublishArtifactResult{Artifact: artifact, Version: version, CommittedAt: receipt.CommittedAt}, nil
}
