package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var capabilities = map[string]struct{}{
	CapabilityOrganizationAdmin: {},
	CapabilityHumanCreate:       {},
	CapabilityGrantIssue:        {},
	CapabilityGrantRevoke:       {},
	CapabilityAuditRead:         {},
	CapabilityAgentCreate:       {},
	CapabilityAgentPlace:        {},
	CapabilitySpaceCreate:       {},
	CapabilitySpaceRead:         {},
	CapabilitySpaceMembers:      {},
	CapabilitySpaceArchive:      {},
	CapabilityMessageSend:       {},
	CapabilityRunExecute:        {},
	CapabilityComputerPair:      {},
}

func ValidCapability(capability string) bool {
	_, ok := capabilities[capability]
	return ok
}

func (s *Store) IssueGrant(ctx context.Context, params IssueGrantParams) (Grant, error) {
	if !ValidCapability(params.Capability) || (params.Subject.Kind != "human" && params.Subject.Kind != "agent") {
		return Grant{}, ErrGrantInvalid
	}
	fingerprint, err := authorityFingerprint(struct {
		SubjectKind string `json:"subject_kind"`
		SubjectID   string `json:"subject_id"`
		Capability  string `json:"capability"`
		ScopeKind   string `json:"scope_kind"`
		ScopeID     string `json:"scope_id"`
		ParentGrant string `json:"parent_grant_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}{params.Subject.Kind, params.Subject.ID, params.Capability, params.Scope.Kind, params.Scope.ID, params.ParentGrantID, optionalUnixNano(params.ExpiresAt)})
	if err != nil {
		return Grant{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, fmt.Errorf("begin grant issue: %w", err)
	}
	defer tx.Rollback()
	if grant, found, err := replayGrantRequest(ctx, tx, "grant_issue_requests", params.RequestID, fingerprint, ErrGrantRequestConflict); err != nil || found {
		return commitGrantReplay(tx, grant, found, err)
	}
	organizationScope := Scope{Kind: "organization", ID: params.Actor.OrganizationID}
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityGrantIssue, organizationScope, params.Now, ""); err != nil {
		return Grant{}, err
	} else if reason != "" {
		return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantIssue, "grant", "", params.RequestID, reason, params.Now)
	}
	if err := validateGrantSubject(ctx, tx, params.Actor.OrganizationID, params.Subject); err != nil {
		return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantIssue, "grant", "", params.RequestID, "subject_invalid", params.Now)
	}
	if err := validateGrantScope(ctx, tx, params.Actor.OrganizationID, params.Scope); err != nil {
		return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantIssue, "grant", "", params.RequestID, "scope_invalid", params.Now)
	}
	parent, err := grantByID(ctx, tx, params.ParentGrantID)
	if err != nil || parent.OrganizationID != params.Actor.OrganizationID || parent.Subject.Kind != params.Actor.Kind || parent.Subject.ID != params.Actor.ID {
		return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantIssue, "grant", "", params.RequestID, "parent_invalid", params.Now)
	}
	grants, err := loadOrganizationGrants(ctx, tx, params.Actor.OrganizationID)
	if err != nil {
		return Grant{}, err
	}
	if !grantEffective(ctx, tx, parent.ID, grants, params.Now, "", map[string]bool{}) {
		return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantIssue, "grant", "", params.RequestID, "parent_inactive", params.Now)
	}
	if !grantAllows(parent, params.Capability, params.Scope) || exceedsExpiry(parent.ExpiresAt, params.ExpiresAt, params.Now) {
		return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantIssue, "grant", "", params.RequestID, "grant_expansion", params.Now)
	}
	if params.Capability == CapabilityOrganizationAdmin {
		if params.Subject.Kind != "human" || !humanIsOwner(ctx, tx, params.Subject.ID) {
			return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantIssue, "grant", "", params.RequestID, "admin_subject_invalid", params.Now)
		}
	}

	id := uuid.NewString()
	stamp := unixNano(params.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO grants(
			id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
			capability, scope_kind, scope_id, parent_grant_id, expires_at, created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, params.Actor.OrganizationID, params.Subject.Kind, params.Subject.ID, params.Actor.Kind,
		params.Actor.ID, params.Capability, params.Scope.Kind, params.Scope.ID, parent.ID,
		nullableUnixNano(params.ExpiresAt), stamp, stamp); err != nil {
		return Grant{}, fmt.Errorf("persist grant: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO grant_issue_requests(request_id, grant_id, payload_fingerprint)
		VALUES(?, ?, ?)
	`, params.RequestID, id, fingerprint[:]); err != nil {
		return Grant{}, fmt.Errorf("persist grant issue request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditGrantIssue, TargetKind: "grant", TargetID: id, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return Grant{}, err
	}
	grant, err := grantByID(ctx, tx, id)
	if err != nil {
		return Grant{}, fmt.Errorf("read issued grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, fmt.Errorf("commit grant issue: %w", err)
	}
	return grant, nil
}

func (s *Store) RevokeGrant(ctx context.Context, params RevokeGrantParams) (Grant, error) {
	fingerprint, err := authorityFingerprint(struct {
		GrantID string `json:"grant_id"`
	}{params.GrantID})
	if err != nil {
		return Grant{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, fmt.Errorf("begin grant revoke: %w", err)
	}
	defer tx.Rollback()
	if grant, found, err := replayGrantRequest(ctx, tx, "grant_revoke_requests", params.RequestID, fingerprint, ErrGrantRevokeConflict); err != nil || found {
		return commitGrantReplay(tx, grant, found, err)
	}
	organizationScope := Scope{Kind: "organization", ID: params.Actor.OrganizationID}
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityGrantRevoke, organizationScope, params.Now, ""); err != nil {
		return Grant{}, err
	} else if reason != "" {
		return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantRevoke, "grant", params.GrantID, params.RequestID, reason, params.Now)
	}
	grant, err := grantByID(ctx, tx, params.GrantID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && grant.OrganizationID != params.Actor.OrganizationID) {
		return Grant{}, ErrGrantNotFound
	}
	if err != nil {
		return Grant{}, fmt.Errorf("read grant for revoke: %w", err)
	}
	if grant.RevokedAt != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO grant_revoke_requests(request_id, grant_id, payload_fingerprint) VALUES(?, ?, ?)`, params.RequestID, grant.ID, fingerprint[:]); err != nil {
			return Grant{}, fmt.Errorf("persist idempotent grant revoke request: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Grant{}, fmt.Errorf("commit idempotent grant revoke: %w", err)
		}
		return grant, nil
	}
	if grant.Capability == CapabilityOrganizationAdmin && grant.Subject.Kind == "human" {
		recoverable, err := hasRecoverableOwner(ctx, tx, params.Now, "", grant.ID)
		if err != nil {
			return Grant{}, err
		}
		if !recoverable {
			return Grant{}, commitDenied(ctx, tx, params.Actor, AuditGrantRevoke, "grant", grant.ID, params.RequestID, "last_owner", params.Now)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE grants SET revoked_at = ?, updated_at = max(updated_at, ?) WHERE id = ?", unixNano(params.Now), unixNano(params.Now), grant.ID); err != nil {
		return Grant{}, fmt.Errorf("persist grant revoke: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO grant_revoke_requests(request_id, grant_id, payload_fingerprint) VALUES(?, ?, ?)`, params.RequestID, grant.ID, fingerprint[:]); err != nil {
		return Grant{}, fmt.Errorf("persist grant revoke request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditGrantRevoke, TargetKind: "grant", TargetID: grant.ID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return Grant{}, err
	}
	grant, err = grantByID(ctx, tx, grant.ID)
	if err != nil {
		return Grant{}, fmt.Errorf("read revoked grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, fmt.Errorf("commit grant revoke: %w", err)
	}
	return grant, nil
}

func (s *Store) GetGrant(ctx context.Context, id string) (Grant, error) {
	grant, err := scanGrant(s.db.QueryRowContext(ctx, grantSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrGrantNotFound
	}
	if err != nil {
		return Grant{}, fmt.Errorf("get grant: %w", err)
	}
	return grant, nil
}

func (s *Store) ListGrants(ctx context.Context, organizationID string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, grantSelect+" WHERE organization_id = ? ORDER BY created_at, id", organizationID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()
	var grants []Grant
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan grant: %w", err)
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grants: %w", err)
	}
	return grants, nil
}

func (s *Store) CheckPermission(ctx context.Context, subject Principal, capability string, scope Scope, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin permission check: %w", err)
	}
	defer tx.Rollback()
	reason, err := requireGrant(ctx, tx, subject, capability, scope, now, "")
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit permission check: %w", err)
	}
	return reason == "", nil
}

func requireGrant(ctx context.Context, tx *sql.Tx, subject Principal, capability string, scope Scope, now time.Time, excludedGrantID string) (string, error) {
	active, err := principalActive(ctx, tx, subject)
	if err != nil {
		return "", err
	}
	if !active {
		return "principal_inactive", nil
	}
	grants, err := loadOrganizationGrants(ctx, tx, subject.OrganizationID)
	if err != nil {
		return "", err
	}
	for id, grant := range grants {
		if grant.Subject.Kind != subject.Kind || grant.Subject.ID != subject.ID || !grantAllows(grant, capability, scope) {
			continue
		}
		if grantEffective(ctx, tx, id, grants, now, excludedGrantID, map[string]bool{}) {
			return "", nil
		}
	}
	return "permission_missing", nil
}

func loadOrganizationGrants(ctx context.Context, tx *sql.Tx, organizationID string) (map[string]Grant, error) {
	rows, err := tx.QueryContext(ctx, grantSelect+" WHERE organization_id = ?", organizationID)
	if err != nil {
		return nil, fmt.Errorf("load organization grants: %w", err)
	}
	defer rows.Close()
	grants := make(map[string]Grant)
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization grant: %w", err)
		}
		grants[grant.ID] = grant
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization grants: %w", err)
	}
	return grants, nil
}

func grantEffective(ctx context.Context, tx *sql.Tx, id string, grants map[string]Grant, now time.Time, excludedGrantID string, visiting map[string]bool) bool {
	grant, ok := grants[id]
	if !ok || id == excludedGrantID || visiting[id] || grant.RevokedAt != nil || (grant.ExpiresAt != nil && !grant.ExpiresAt.After(now)) {
		return false
	}
	active, err := principalActive(ctx, tx, grant.Subject)
	if err != nil || !active {
		return false
	}
	if grant.ParentGrantID == "" {
		return grant.Issuer.Kind == "system" && grant.Capability == CapabilityOrganizationAdmin && grant.Scope.Kind == "organization" && grant.Scope.ID == grant.OrganizationID
	}
	parent, ok := grants[grant.ParentGrantID]
	if !ok || parent.Subject.Kind != grant.Issuer.Kind || parent.Subject.ID != grant.Issuer.ID || !grantAllows(parent, grant.Capability, grant.Scope) {
		return false
	}
	visiting[id] = true
	effective := grantEffective(ctx, tx, parent.ID, grants, now, excludedGrantID, visiting)
	delete(visiting, id)
	return effective
}

func grantAllows(grant Grant, capability string, scope Scope) bool {
	if grant.Capability != CapabilityOrganizationAdmin && grant.Capability != capability {
		return false
	}
	return grant.Scope.Kind == "organization" && grant.Scope.ID == grant.OrganizationID || grant.Scope.Kind == scope.Kind && grant.Scope.ID == scope.ID
}

func principalActive(ctx context.Context, tx *sql.Tx, principal Principal) (bool, error) {
	switch principal.Kind {
	case "human":
		var active bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM humans WHERE id = ? AND organization_id = ? AND status = 'active')", principal.ID, principal.OrganizationID).Scan(&active); err != nil {
			return false, fmt.Errorf("check human principal: %w", err)
		}
		return active, nil
	case "agent":
		return recordExists(ctx, tx, "agents", principal.ID)
	case "system":
		return principal.ID == "", nil
	default:
		return false, nil
	}
}

func validateGrantSubject(ctx context.Context, tx *sql.Tx, organizationID string, subject Principal) error {
	subject.OrganizationID = organizationID
	active, err := principalActive(ctx, tx, subject)
	if err != nil {
		return err
	}
	if !active {
		return ErrPrincipalNotFound
	}
	return nil
}

func validateGrantScope(ctx context.Context, tx *sql.Tx, organizationID string, scope Scope) error {
	if scope.Kind == "organization" {
		if scope.ID != organizationID {
			return ErrScopeNotFound
		}
		return nil
	}
	table := map[string]string{"agent": "agents", "computer": "computers", "space": "spaces"}[scope.Kind]
	if table == "" {
		return ErrScopeNotFound
	}
	var tableExists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", table).Scan(&tableExists); err != nil {
		return err
	}
	if !tableExists {
		return ErrScopeNotFound
	}
	exists, err := recordExists(ctx, tx, table, scope.ID)
	if err != nil || !exists {
		return ErrScopeNotFound
	}
	return nil
}

func hasRecoverableOwner(ctx context.Context, tx *sql.Tx, now time.Time, excludedHumanID, excludedGrantID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, organization_id FROM humans WHERE role = 'owner' AND status = 'active' AND id != ?", excludedHumanID)
	if err != nil {
		return false, fmt.Errorf("list recoverable owners: %w", err)
	}
	var owners []Principal
	for rows.Next() {
		var owner Principal
		owner.Kind = "human"
		if err := rows.Scan(&owner.ID, &owner.OrganizationID); err != nil {
			rows.Close()
			return false, fmt.Errorf("scan recoverable owner: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, owner := range owners {
		reason, err := requireGrant(ctx, tx, owner, CapabilityOrganizationAdmin, Scope{Kind: "organization", ID: owner.OrganizationID}, now, excludedGrantID)
		if err != nil {
			return false, err
		}
		if reason == "" {
			return true, nil
		}
	}
	return false, nil
}

func humanIsOwner(ctx context.Context, tx *sql.Tx, id string) bool {
	var owner bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM humans WHERE id = ? AND role = 'owner' AND status = 'active')", id).Scan(&owner); err != nil {
		return false
	}
	return owner
}

func exceedsExpiry(parent, child *time.Time, now time.Time) bool {
	if child != nil && !child.After(now) {
		return true
	}
	if parent == nil {
		return false
	}
	return child == nil || child.After(*parent)
}

func nullableUnixNano(value *time.Time) any {
	if value == nil {
		return nil
	}
	return unixNano(*value)
}

func optionalUnixNano(value *time.Time) int64 {
	if value == nil {
		return 0
	}
	return unixNano(*value)
}

const grantSelect = `
	SELECT id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
	       capability, scope_kind, scope_id, parent_grant_id, expires_at, revoked_at,
	       created_at, updated_at
	FROM grants`

func grantByID(ctx context.Context, tx *sql.Tx, id string) (Grant, error) {
	return scanGrant(tx.QueryRowContext(ctx, grantSelect+" WHERE id = ?", id))
}

func scanGrant(row scanner) (Grant, error) {
	var grant Grant
	var expiresAt, revokedAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&grant.ID, &grant.OrganizationID, &grant.Subject.Kind, &grant.Subject.ID,
		&grant.Issuer.Kind, &grant.Issuer.ID, &grant.Capability, &grant.Scope.Kind, &grant.Scope.ID,
		&grant.ParentGrantID, &expiresAt, &revokedAt, &createdAt, &updatedAt); err != nil {
		return Grant{}, err
	}
	grant.Subject.OrganizationID = grant.OrganizationID
	grant.Issuer.OrganizationID = grant.OrganizationID
	if expiresAt.Valid {
		value := timeFromUnixNano(expiresAt.Int64)
		grant.ExpiresAt = &value
	}
	if revokedAt.Valid {
		value := timeFromUnixNano(revokedAt.Int64)
		grant.RevokedAt = &value
	}
	grant.CreatedAt = timeFromUnixNano(createdAt)
	grant.UpdatedAt = timeFromUnixNano(updatedAt)
	return grant, nil
}

func replayGrantRequest(ctx context.Context, tx *sql.Tx, table, requestID string, fingerprint [sha256.Size]byte, conflict error) (Grant, bool, error) {
	var grantID string
	var stored []byte
	err := tx.QueryRowContext(ctx, "SELECT grant_id, payload_fingerprint FROM "+table+" WHERE request_id = ?", requestID).Scan(&grantID, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, false, nil
	}
	if err != nil {
		return Grant{}, false, fmt.Errorf("read grant request receipt: %w", err)
	}
	if !bytes.Equal(stored, fingerprint[:]) {
		return Grant{}, false, conflict
	}
	grant, err := grantByID(ctx, tx, grantID)
	if err != nil {
		return Grant{}, false, fmt.Errorf("read grant request result: %w", err)
	}
	return grant, true, nil
}

func commitGrantReplay(tx *sql.Tx, grant Grant, found bool, err error) (Grant, error) {
	if err != nil {
		return Grant{}, err
	}
	if !found {
		return Grant{}, errors.New("grant replay called without receipt")
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, fmt.Errorf("commit grant request replay: %w", err)
	}
	return grant, nil
}
