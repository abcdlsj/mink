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

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/google/uuid"
)

const (
	CapabilityOrganizationAdmin = authoritydomain.CapabilityOrganizationAdmin
	CapabilityHumanCreate       = authoritydomain.CapabilityHumanCreate
	CapabilityGrantIssue        = authoritydomain.CapabilityGrantIssue
	CapabilityGrantRevoke       = authoritydomain.CapabilityGrantRevoke
	CapabilityAuditRead         = authoritydomain.CapabilityAuditRead
	CapabilityAgentCreate       = authoritydomain.CapabilityAgentCreate
	CapabilityAgentPlace        = authoritydomain.CapabilityAgentPlace
	CapabilitySpaceCreate       = authoritydomain.CapabilitySpaceCreate
	CapabilitySpaceRead         = authoritydomain.CapabilitySpaceRead
	CapabilitySpaceMembers      = authoritydomain.CapabilitySpaceMembers
	CapabilitySpaceArchive      = authoritydomain.CapabilitySpaceArchive
	CapabilityMessageSend       = authoritydomain.CapabilityMessageSend
	CapabilityRunExecute        = authoritydomain.CapabilityRunExecute
	CapabilityComputerPair      = authoritydomain.CapabilityComputerPair
	CapabilityWorkCreate        = authoritydomain.CapabilityWorkCreate
	CapabilityWorkRead          = authoritydomain.CapabilityWorkRead
	CapabilityWorkManage        = authoritydomain.CapabilityWorkManage
	CapabilityWorkApprove       = authoritydomain.CapabilityWorkApprove
)

type Organization struct {
	ID               string
	Name             string
	BootstrapHumanID string
	CreatedAt        time.Time
}

type Human struct {
	ID             string
	OrganizationID string
	Name           string
	Role           string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Principal = authoritydomain.Principal

type PrincipalKind = authoritydomain.PrincipalKind

type Scope = authoritydomain.Scope

type ScopeKind = authoritydomain.ScopeKind

type Capability = authoritydomain.Capability

type Grant = grantapp.Grant

type AuthorityBootstrap struct {
	Organization Organization
	Human        Human
	RootGrant    Grant
}

type CreateHumanParams struct {
	RequestID  string
	Actor      Principal
	Name       string
	Role       string
	Credential string
	Now        time.Time
}

type SetHumanStatusParams struct {
	RequestID string
	Actor     Principal
	HumanID   string
	Status    string
	Now       time.Time
}

type IssueGrantParams = grantapp.IssueCommand

type RevokeGrantParams = grantapp.RevokeCommand

func (s *Store) AuthorityExists(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM organizations").Scan(&count); err != nil {
		return false, fmt.Errorf("check authority bootstrap: %w", err)
	}
	return count > 0, nil
}

func (s *Store) EnsureAuthority(ctx context.Context, credential string, now time.Time) (AuthorityBootstrap, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("begin authority bootstrap: %w", err)
	}
	defer tx.Rollback()

	bootstrap, err := readAuthorityBootstrap(ctx, tx)
	if err == nil {
		if err := verifyBootstrapCredential(ctx, tx, bootstrap.Human.ID, credential); err != nil {
			return AuthorityBootstrap{}, err
		}
		if err := tx.Commit(); err != nil {
			return AuthorityBootstrap{}, fmt.Errorf("commit authority verification: %w", err)
		}
		return bootstrap, nil
	}
	if !errors.Is(err, ErrAuthorityNotBootstrapped) {
		return AuthorityBootstrap{}, err
	}

	organizationID := uuid.NewString()
	humanID := uuid.NewString()
	grantID := uuid.NewString()
	stamp := unixNano(now)
	keyHash := sha256.Sum256([]byte(credential))
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO organizations(singleton, id, name, bootstrap_human_id, created_at)
		VALUES(1, ?, 'Sumi', ?, ?)
	`, organizationID, humanID, stamp); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("persist organization bootstrap: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO humans(id, organization_id, name, role, status, credential_hash, created_at, updated_at)
		VALUES(?, ?, 'Owner', 'owner', 'active', ?, ?, ?)
	`, humanID, organizationID, keyHash[:], stamp, stamp); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("persist bootstrap human: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO grants(
			id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
			capability, scope_kind, scope_id, parent_grant_id, created_at, updated_at
		)
		VALUES(?, ?, 'human', ?, 'system', '', ?, 'organization', ?, '', ?, ?)
	`, grantID, organizationID, humanID, CapabilityOrganizationAdmin, organizationID, stamp, stamp); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("persist bootstrap grant: %w", err)
	}
	system := Principal{Kind: "system", OrganizationID: organizationID}
	for _, event := range []AppendAuditParams{
		{OrganizationID: organizationID, Actor: system, Action: AuditOrganizationBootstrap, TargetKind: "organization", TargetID: organizationID, Outcome: "committed", Now: now},
		{OrganizationID: organizationID, Actor: system, Action: AuditHumanCreate, TargetKind: "human", TargetID: humanID, Outcome: "committed", Now: now},
		{OrganizationID: organizationID, Actor: system, Action: AuditGrantIssue, TargetKind: "grant", TargetID: grantID, Outcome: "committed", Now: now},
	} {
		if err := appendAuditEvent(ctx, tx, event); err != nil {
			return AuthorityBootstrap{}, err
		}
	}
	bootstrap, err = readAuthorityBootstrap(ctx, tx)
	if err != nil {
		return AuthorityBootstrap{}, err
	}
	if err := tx.Commit(); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("commit authority bootstrap: %w", err)
	}
	return bootstrap, nil
}

func (s *Store) AuthenticateHuman(ctx context.Context, credential string) (Principal, error) {
	keyHash := sha256.Sum256([]byte(credential))
	var principal Principal
	var status string
	err := s.db.QueryRowContext(ctx, `
		SELECT 'human', id, organization_id, status
		FROM humans
		WHERE credential_hash = ?
	`, keyHash[:]).Scan(&principal.Kind, &principal.ID, &principal.OrganizationID, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != "active") {
		return Principal{}, ErrPermissionDenied
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate human: %w", err)
	}
	return principal, nil
}

func (s *Store) GetOrganization(ctx context.Context) (Organization, error) {
	return scanOrganization(s.db.QueryRowContext(ctx, `
		SELECT id, name, bootstrap_human_id, created_at
		FROM organizations WHERE singleton = 1
	`))
}

func (s *Store) GetHuman(ctx context.Context, id string) (Human, error) {
	human, err := scanHuman(s.db.QueryRowContext(ctx, humanSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Human{}, ErrHumanNotFound
	}
	if err != nil {
		return Human{}, fmt.Errorf("get human: %w", err)
	}
	return human, nil
}

func (s *Store) ListHumans(ctx context.Context, organizationID string) ([]Human, error) {
	rows, err := s.db.QueryContext(ctx, humanSelect+" WHERE organization_id = ? ORDER BY name, id", organizationID)
	if err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	defer rows.Close()
	var humans []Human
	for rows.Next() {
		human, err := scanHuman(rows)
		if err != nil {
			return nil, fmt.Errorf("scan human: %w", err)
		}
		humans = append(humans, human)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate humans: %w", err)
	}
	return humans, nil
}

func (s *Store) CreateHuman(ctx context.Context, params CreateHumanParams) (Human, error) {
	fingerprint, err := authorityFingerprint(struct {
		Name       string `json:"name"`
		Role       string `json:"role"`
		Credential string `json:"credential"`
	}{params.Name, params.Role, params.Credential})
	if err != nil {
		return Human{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Human{}, fmt.Errorf("begin human creation: %w", err)
	}
	defer tx.Rollback()
	if human, found, err := replayHumanRequest(ctx, tx, params.RequestID, fingerprint); err != nil || found {
		return commitHumanReplay(tx, human, found, err)
	}
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityHumanCreate, Scope{Kind: "organization", ID: params.Actor.OrganizationID}, params.Now, ""); err != nil {
		return Human{}, err
	} else if reason != "" {
		return Human{}, commitDenied(ctx, tx, params.Actor, AuditHumanCreate, "human", "", params.RequestID, reason, params.Now)
	}

	id := uuid.NewString()
	stamp := unixNano(params.Now)
	keyHash := sha256.Sum256([]byte(params.Credential))
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO humans(id, organization_id, name, role, status, credential_hash, created_at, updated_at)
		VALUES(?, ?, ?, ?, 'active', ?, ?, ?)
	`, id, params.Actor.OrganizationID, params.Name, params.Role, keyHash[:], stamp, stamp); err != nil {
		if isUniqueConstraint(err, "humans.organization_id, humans.name") {
			return Human{}, ErrHumanNameExists
		}
		if isUniqueConstraint(err, "humans.credential_hash") {
			return Human{}, ErrHumanCredentialExists
		}
		return Human{}, fmt.Errorf("persist human: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO human_create_requests(request_id, human_id, payload_fingerprint)
		VALUES(?, ?, ?)
	`, params.RequestID, id, fingerprint[:]); err != nil {
		return Human{}, fmt.Errorf("persist human creation request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditHumanCreate, TargetKind: "human", TargetID: id, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return Human{}, err
	}
	human, err := scanHuman(tx.QueryRowContext(ctx, humanSelect+" WHERE id = ?", id))
	if err != nil {
		return Human{}, fmt.Errorf("read created human: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Human{}, fmt.Errorf("commit human creation: %w", err)
	}
	return human, nil
}

func (s *Store) SetHumanStatus(ctx context.Context, params SetHumanStatusParams) (Human, error) {
	fingerprint, err := authorityFingerprint(struct {
		HumanID string `json:"human_id"`
		Status  string `json:"status"`
	}{params.HumanID, params.Status})
	if err != nil {
		return Human{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Human{}, fmt.Errorf("begin human status: %w", err)
	}
	defer tx.Rollback()
	if human, found, err := replayHumanStatus(ctx, tx, params.RequestID, fingerprint); err != nil || found {
		return commitHumanReplay(tx, human, found, err)
	}
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityOrganizationAdmin, Scope{Kind: "organization", ID: params.Actor.OrganizationID}, params.Now, ""); err != nil {
		return Human{}, err
	} else if reason != "" {
		return Human{}, commitDenied(ctx, tx, params.Actor, AuditHumanStatusSet, "human", params.HumanID, params.RequestID, reason, params.Now)
	}
	human, err := scanHuman(tx.QueryRowContext(ctx, humanSelect+" WHERE id = ? AND organization_id = ?", params.HumanID, params.Actor.OrganizationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Human{}, ErrHumanNotFound
	}
	if err != nil {
		return Human{}, fmt.Errorf("read human for status: %w", err)
	}
	if human.Status == params.Status {
		if _, err := tx.ExecContext(ctx, `INSERT INTO human_status_requests(request_id, human_id, payload_fingerprint) VALUES(?, ?, ?)`, params.RequestID, human.ID, fingerprint[:]); err != nil {
			return Human{}, fmt.Errorf("persist idempotent human status request: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Human{}, fmt.Errorf("commit idempotent human status: %w", err)
		}
		return human, nil
	}
	if params.Status == "disabled" && human.Role == "owner" {
		recoverable, err := hasRecoverableOwner(ctx, tx, params.Now, human.ID, "")
		if err != nil {
			return Human{}, err
		}
		if !recoverable {
			return Human{}, commitDenied(ctx, tx, params.Actor, AuditHumanStatusSet, "human", human.ID, params.RequestID, "last_owner", params.Now)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE humans SET status = ?, updated_at = max(updated_at, ?) WHERE id = ?", params.Status, unixNano(params.Now), human.ID); err != nil {
		return Human{}, fmt.Errorf("persist human status: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO human_status_requests(request_id, human_id, payload_fingerprint) VALUES(?, ?, ?)`, params.RequestID, human.ID, fingerprint[:]); err != nil {
		return Human{}, fmt.Errorf("persist human status request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditHumanStatusSet, TargetKind: "human", TargetID: human.ID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return Human{}, err
	}
	human, err = scanHuman(tx.QueryRowContext(ctx, humanSelect+" WHERE id = ?", human.ID))
	if err != nil {
		return Human{}, fmt.Errorf("read updated human: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Human{}, fmt.Errorf("commit human status: %w", err)
	}
	return human, nil
}

func readAuthorityBootstrap(ctx context.Context, tx *sql.Tx) (AuthorityBootstrap, error) {
	organization, err := scanOrganization(tx.QueryRowContext(ctx, `SELECT id, name, bootstrap_human_id, created_at FROM organizations WHERE singleton = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		var humans, grants int
		if err := tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM humans), (SELECT count(*) FROM grants)").Scan(&humans, &grants); err != nil {
			return AuthorityBootstrap{}, fmt.Errorf("check partial authority bootstrap: %w", err)
		}
		if humans != 0 || grants != 0 {
			return AuthorityBootstrap{}, ErrAuthorityMismatch
		}
		return AuthorityBootstrap{}, ErrAuthorityNotBootstrapped
	}
	if err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("read organization bootstrap: %w", err)
	}
	human, err := scanHuman(tx.QueryRowContext(ctx, humanSelect+" WHERE id = ?", organization.BootstrapHumanID))
	if err != nil {
		return AuthorityBootstrap{}, ErrAuthorityMismatch
	}
	grant, err := scanGrant(tx.QueryRowContext(ctx, grantSelect+` WHERE organization_id = ? AND subject_kind = 'human' AND subject_id = ? AND issuer_kind = 'system' AND capability = ? AND scope_kind = 'organization' AND scope_id = ? AND parent_grant_id = ''`, organization.ID, human.ID, CapabilityOrganizationAdmin, organization.ID))
	if err != nil || human.OrganizationID != organization.ID || human.Role != "owner" {
		return AuthorityBootstrap{}, ErrAuthorityMismatch
	}
	return AuthorityBootstrap{Organization: organization, Human: human, RootGrant: grant}, nil
}

func verifyBootstrapCredential(ctx context.Context, tx *sql.Tx, humanID, credential string) error {
	keyHash := sha256.Sum256([]byte(credential))
	var matches bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM humans WHERE id = ? AND credential_hash = ?)", humanID, keyHash[:]).Scan(&matches); err != nil {
		return fmt.Errorf("verify bootstrap credential: %w", err)
	}
	if !matches {
		return ErrAuthorityMismatch
	}
	return nil
}

const humanSelect = `SELECT id, organization_id, name, role, status, created_at, updated_at FROM humans`

func scanOrganization(row scanner) (Organization, error) {
	var organization Organization
	var createdAt int64
	if err := row.Scan(&organization.ID, &organization.Name, &organization.BootstrapHumanID, &createdAt); err != nil {
		return Organization{}, err
	}
	organization.CreatedAt = timeFromUnixNano(createdAt)
	return organization, nil
}

func scanHuman(row scanner) (Human, error) {
	var human Human
	var createdAt, updatedAt int64
	if err := row.Scan(&human.ID, &human.OrganizationID, &human.Name, &human.Role, &human.Status, &createdAt, &updatedAt); err != nil {
		return Human{}, err
	}
	human.CreatedAt = timeFromUnixNano(createdAt)
	human.UpdatedAt = timeFromUnixNano(updatedAt)
	return human, nil
}

func authorityFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode authority request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func replayHumanRequest(ctx context.Context, tx *sql.Tx, requestID string, fingerprint [sha256.Size]byte) (Human, bool, error) {
	return replayHuman(ctx, tx, "human_create_requests", requestID, fingerprint, ErrHumanRequestConflict)
}

func replayHumanStatus(ctx context.Context, tx *sql.Tx, requestID string, fingerprint [sha256.Size]byte) (Human, bool, error) {
	return replayHuman(ctx, tx, "human_status_requests", requestID, fingerprint, ErrHumanStatusConflict)
}

func replayHuman(ctx context.Context, tx *sql.Tx, table, requestID string, fingerprint [sha256.Size]byte, conflict error) (Human, bool, error) {
	var humanID string
	var stored []byte
	err := tx.QueryRowContext(ctx, "SELECT human_id, payload_fingerprint FROM "+table+" WHERE request_id = ?", requestID).Scan(&humanID, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Human{}, false, nil
	}
	if err != nil {
		return Human{}, false, fmt.Errorf("read human request receipt: %w", err)
	}
	if !bytes.Equal(stored, fingerprint[:]) {
		return Human{}, false, conflict
	}
	human, err := scanHuman(tx.QueryRowContext(ctx, humanSelect+" WHERE id = ?", humanID))
	if err != nil {
		return Human{}, false, fmt.Errorf("read human request result: %w", err)
	}
	return human, true, nil
}

func commitHumanReplay(tx *sql.Tx, human Human, found bool, err error) (Human, error) {
	if err != nil {
		return Human{}, err
	}
	if !found {
		return Human{}, errors.New("human replay called without receipt")
	}
	if err := tx.Commit(); err != nil {
		return Human{}, fmt.Errorf("commit human request replay: %w", err)
	}
	return human, nil
}

func commitDenied(ctx context.Context, tx *sql.Tx, actor Principal, action, targetKind, targetID, requestID, reason string, now time.Time) error {
	return commitDeniedWithContext(ctx, tx, actor, action, targetKind, targetID, "", "", requestID, reason, now)
}

func commitDeniedWithContext(ctx context.Context, tx *sql.Tx, actor Principal, action, targetKind, targetID, contextKind, contextID, requestID, reason string, now time.Time) error {
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: actor.OrganizationID, Actor: actor, Action: action, TargetKind: targetKind, TargetID: targetID, ContextKind: contextKind, ContextID: contextID, RequestID: requestID, Outcome: "denied", ReasonCode: reason, Now: now}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit denied authority action: %w", err)
	}
	return ErrPermissionDenied
}

func isUniqueConstraint(err error, fragment string) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte(fragment))
}
