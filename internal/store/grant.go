package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/google/uuid"
)

func (s *Store) IssueGrant(ctx context.Context, p IssueGrantParams) (Grant, error) {
	if !p.Capability.Valid() || (p.Subject.Kind != authoritydomain.PrincipalHuman && p.Subject.Kind != authoritydomain.PrincipalAgent) {
		return Grant{}, ErrGrantInvalid
	}
	if !p.Capability.AllowsScope(p.Scope.Kind) {
		return Grant{}, ErrGrantInvalid
	}
	fp, err := authorityFingerprint(struct {
		SubjectKind PrincipalKind `json:"subject_kind"`
		SubjectID   string        `json:"subject_id"`
		Capability  Capability    `json:"capability"`
		ScopeKind   ScopeKind     `json:"scope_kind"`
		ScopeID     string        `json:"scope_id"`
		ParentGrant string        `json:"parent_grant_id"`
		ExpiresAt   int64         `json:"expires_at"`
	}{p.Subject.Kind, p.Subject.ID, p.Capability, p.Scope.Kind, p.Scope.ID, p.ParentGrantID, optionalUnixNano(p.ExpiresAt)})
	if err != nil {
		return Grant{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, fmt.Errorf("begin grant issue: %w", err)
	}
	defer tx.Rollback()
	if g, found, err := replayGrantRequest(ctx, tx, "grant_issue_requests", p.RequestID, fp, ErrGrantRequestConflict); err != nil || found {
		return commitGrantReplay(tx, g, found, err)
	}
	orgScope := Scope{Kind: "organization", ID: p.Actor.OrganizationID}
	if reason, err := requireGrant(ctx, tx, p.Actor, CapabilityGrantIssue, orgScope, p.Now, ""); err != nil {
		return Grant{}, err
	} else if reason != "" {
		return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantIssue, "grant", "", p.RequestID, reason, p.Now)
	}
	if err := validateGrantSubject(ctx, tx, p.Actor.OrganizationID, p.Subject); err != nil {
		return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantIssue, "grant", "", p.RequestID, "subject_invalid", p.Now)
	}
	if err := validateGrantScope(ctx, tx, p.Actor.OrganizationID, p.Scope); err != nil {
		return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantIssue, "grant", "", p.RequestID, "scope_invalid", p.Now)
	}
	parent, err := grantByID(ctx, tx, p.ParentGrantID)
	if err != nil || parent.OrganizationID != p.Actor.OrganizationID || parent.Subject.Kind != p.Actor.Kind || parent.Subject.ID != p.Actor.ID {
		return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantIssue, "grant", "", p.RequestID, "parent_invalid", p.Now)
	}
	gs, err := loadOrganizationGrants(ctx, tx, p.Actor.OrganizationID)
	if err != nil {
		return Grant{}, err
	}
	if !grantEffective(ctx, tx, parent.ID, gs, p.Now, "", map[string]bool{}) {
		return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantIssue, "grant", "", p.RequestID, "parent_inactive", p.Now)
	}
	if !grantAllows(parent, p.Capability, p.Scope) || exceedsExpiry(parent.ExpiresAt, p.ExpiresAt, p.Now) {
		return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantIssue, "grant", "", p.RequestID, "grant_expansion", p.Now)
	}
	if p.Capability == CapabilityOrganizationAdmin {
		if p.Subject.Kind != "human" || !humanIsOwner(ctx, tx, p.Subject.ID) {
			return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantIssue, "grant", "", p.RequestID, "admin_subject_invalid", p.Now)
		}
	}

	id := uuid.NewString()
	stamp := unixNano(p.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO grants(
			id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
			capability, scope_kind, scope_id, parent_grant_id, expires_at, created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, p.Actor.OrganizationID, p.Subject.Kind, p.Subject.ID, p.Actor.Kind,
		p.Actor.ID, p.Capability, p.Scope.Kind, p.Scope.ID, parent.ID,
		nullableUnixNano(p.ExpiresAt), stamp, stamp); err != nil {
		return Grant{}, fmt.Errorf("persist grant: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO grant_issue_requests(request_id, grant_id, payload_fingerprint)
		VALUES(?, ?, ?)
	`, p.RequestID, id, fp[:]); err != nil {
		return Grant{}, fmt.Errorf("persist grant issue request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: p.Actor.OrganizationID, Actor: p.Actor, Action: AuditGrantIssue, TargetKind: "grant", TargetID: id, RequestID: p.RequestID, Outcome: "committed", Now: p.Now}); err != nil {
		return Grant{}, err
	}
	g, err := grantByID(ctx, tx, id)
	if err != nil {
		return Grant{}, fmt.Errorf("read issued grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, fmt.Errorf("commit grant issue: %w", err)
	}
	return g, nil
}

func (s *Store) RevokeGrant(ctx context.Context, p RevokeGrantParams) (Grant, error) {
	fp, err := authorityFingerprint(struct {
		GrantID string `json:"grant_id"`
	}{p.GrantID})
	if err != nil {
		return Grant{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, fmt.Errorf("begin grant revoke: %w", err)
	}
	defer tx.Rollback()
	if g, found, err := replayGrantRequest(ctx, tx, "grant_revoke_requests", p.RequestID, fp, ErrGrantRevokeConflict); err != nil || found {
		return commitGrantReplay(tx, g, found, err)
	}
	orgScope := Scope{Kind: "organization", ID: p.Actor.OrganizationID}
	if reason, err := requireGrant(ctx, tx, p.Actor, CapabilityGrantRevoke, orgScope, p.Now, ""); err != nil {
		return Grant{}, err
	} else if reason != "" {
		return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantRevoke, "grant", p.GrantID, p.RequestID, reason, p.Now)
	}
	g, err := grantByID(ctx, tx, p.GrantID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && g.OrganizationID != p.Actor.OrganizationID) {
		return Grant{}, ErrGrantNotFound
	}
	if err != nil {
		return Grant{}, fmt.Errorf("read grant for revoke: %w", err)
	}
	if g.RevokedAt != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO grant_revoke_requests(request_id, grant_id, payload_fingerprint) VALUES(?, ?, ?)`, p.RequestID, g.ID, fp[:]); err != nil {
			return Grant{}, fmt.Errorf("persist idempotent grant revoke request: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Grant{}, fmt.Errorf("commit idempotent grant revoke: %w", err)
		}
		return g, nil
	}
	if g.Capability == CapabilityOrganizationAdmin && g.Subject.Kind == "human" {
		ok, err := hasRecoverableOwner(ctx, tx, p.Now, "", g.ID)
		if err != nil {
			return Grant{}, err
		}
		if !ok {
			return Grant{}, commitDenied(ctx, tx, p.Actor, AuditGrantRevoke, "grant", g.ID, p.RequestID, "last_owner", p.Now)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE grants SET revoked_at = ?, updated_at = max(updated_at, ?) WHERE id = ?", unixNano(p.Now), unixNano(p.Now), g.ID); err != nil {
		return Grant{}, fmt.Errorf("persist grant revoke: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO grant_revoke_requests(request_id, grant_id, payload_fingerprint) VALUES(?, ?, ?)`, p.RequestID, g.ID, fp[:]); err != nil {
		return Grant{}, fmt.Errorf("persist grant revoke request: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: p.Actor.OrganizationID, Actor: p.Actor, Action: AuditGrantRevoke, TargetKind: "grant", TargetID: g.ID, RequestID: p.RequestID, Outcome: "committed", Now: p.Now}); err != nil {
		return Grant{}, err
	}
	g, err = grantByID(ctx, tx, g.ID)
	if err != nil {
		return Grant{}, fmt.Errorf("read revoked grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, fmt.Errorf("commit grant revoke: %w", err)
	}
	return g, nil
}

func (s *Store) GetGrant(ctx context.Context, query grantapp.GetQuery) (Grant, error) {
	grant, err := scanGrant(s.db.QueryRowContext(ctx, grantSelect+" WHERE id = ?", query.GrantID))
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrGrantNotFound
	}
	if err != nil {
		return Grant{}, fmt.Errorf("get grant: %w", err)
	}
	return grant, nil
}

func (s *Store) ListGrants(ctx context.Context, query grantapp.ListQuery) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, grantSelect+" WHERE organization_id = ? ORDER BY created_at, id", query.OrganizationID)
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

type CheckPermissionParams = grantapp.PermissionQuery

func (s *Store) CheckPermission(ctx context.Context, p CheckPermissionParams) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin permission check: %w", err)
	}
	defer tx.Rollback()
	reason, err := requireGrant(ctx, tx, p.Subject, p.Capability, p.Scope, p.Now, "")
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit permission check: %w", err)
	}
	return reason == "", nil
}

func requireGrant(ctx context.Context, tx *sql.Tx, subject Principal, capability Capability, scope Scope, now time.Time, excludedGrantID string) (string, error) {
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

func loadOrganizationGrants(ctx context.Context, tx *sql.Tx, orgID string) (map[string]Grant, error) {
	rows, err := tx.QueryContext(ctx, grantSelect+" WHERE organization_id = ?", orgID)
	if err != nil {
		return nil, fmt.Errorf("load organization grants: %w", err)
	}
	defer rows.Close()
	gs := make(map[string]Grant)
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization grant: %w", err)
		}
		gs[g.ID] = g
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization grants: %w", err)
	}
	return gs, nil
}

func grantEffective(ctx context.Context, tx *sql.Tx, id string, gs map[string]Grant, now time.Time, excludedGrantID string, visiting map[string]bool) bool {
	g, ok := gs[id]
	if !ok || id == excludedGrantID || visiting[id] || g.RevokedAt != nil || (g.ExpiresAt != nil && !g.ExpiresAt.After(now)) {
		return false
	}
	active, err := principalActive(ctx, tx, g.Subject)
	if err != nil || !active {
		return false
	}
	if g.ParentGrantID == "" {
		return g.Issuer.Kind == "system" && g.Capability == CapabilityOrganizationAdmin && g.Scope.Kind == "organization" && g.Scope.ID == g.OrganizationID
	}
	parent, ok := gs[g.ParentGrantID]
	if !ok || parent.Subject.Kind != g.Issuer.Kind || parent.Subject.ID != g.Issuer.ID || !grantAllows(parent, g.Capability, g.Scope) {
		return false
	}
	visiting[id] = true
	effective := grantEffective(ctx, tx, parent.ID, gs, now, excludedGrantID, visiting)
	delete(visiting, id)
	return effective
}

func grantAllows(grant Grant, capability Capability, scope Scope) bool {
	if grant.Capability != CapabilityOrganizationAdmin && grant.Capability != capability {
		return false
	}
	return grant.Scope.Kind == "organization" && grant.Scope.ID == grant.OrganizationID || grant.Scope.Kind == scope.Kind && grant.Scope.ID == scope.ID
}

func principalActive(ctx context.Context, tx *sql.Tx, principal Principal) (bool, error) {
	switch principal.Kind {
	case authoritydomain.PrincipalHuman:
		var active bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM humans WHERE id = ? AND organization_id = ? AND status = 'active')", principal.ID, principal.OrganizationID).Scan(&active); err != nil {
			return false, fmt.Errorf("check human principal: %w", err)
		}
		return active, nil
	case authoritydomain.PrincipalAgent:
		return agentExists(ctx, tx, principal.ID)
	case authoritydomain.PrincipalSystem:
		return principal.ID == "", nil
	default:
		return false, nil
	}
}

func validateGrantSubject(ctx context.Context, tx *sql.Tx, orgID string, sub Principal) error {
	sub.OrganizationID = orgID
	active, err := principalActive(ctx, tx, sub)
	if err != nil {
		return err
	}
	if !active {
		return ErrPrincipalNotFound
	}
	return nil
}

func validateGrantScope(ctx context.Context, tx *sql.Tx, organizationID string, scope Scope) error {
	if scope.Kind == authoritydomain.ScopeOrganization {
		if scope.ID != organizationID {
			return ErrScopeNotFound
		}
		return nil
	}
	var exists bool
	var err error
	switch scope.Kind {
	case authoritydomain.ScopeAgent:
		exists, err = agentExists(ctx, tx, scope.ID)
	case authoritydomain.ScopeComputer:
		exists, err = computerExists(ctx, tx, scope.ID)
	case authoritydomain.ScopeSpace:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM spaces WHERE id = ? AND organization_id = ?)`, scope.ID, organizationID).Scan(&exists)
	case authoritydomain.ScopeWork:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM works WHERE id = ? AND organization_id = ?)`, scope.ID, organizationID).Scan(&exists)
	default:
		return ErrScopeNotFound
	}
	if err != nil {
		return err
	}
	if !exists {
		return ErrScopeNotFound
	}
	return nil
}

type ownerAfterScanKey struct{}

type ownerAfterScanFn func(int, *sql.Rows)

func recoverableOwnerAfterScan(ctx context.Context, n int, rows *sql.Rows) {
	if apply, ok := ctx.Value(ownerAfterScanKey{}).(ownerAfterScanFn); ok {
		apply(n, rows)
	}
}

func hasRecoverableOwner(ctx context.Context, tx *sql.Tx, now time.Time, exHumanID, exGrantID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, organization_id FROM humans WHERE role = 'owner' AND status = 'active' AND id != ?", exHumanID)
	if err != nil {
		return false, fmt.Errorf("list recoverable owners: %w", err)
	}
	var owners []Principal
	for rows.Next() {
		var o Principal
		o.Kind = authoritydomain.PrincipalHuman
		if err := rows.Scan(&o.ID, &o.OrganizationID); err != nil {
			rows.Close()
			return false, fmt.Errorf("scan recoverable owner: %w", err)
		}
		owners = append(owners, o)
		recoverableOwnerAfterScan(ctx, len(owners), rows)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("iterate recoverable owners: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, o := range owners {
		reason, err := requireGrant(ctx, tx, o, CapabilityOrganizationAdmin, Scope{Kind: "organization", ID: o.OrganizationID}, now, exGrantID)
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

func exceedsExpiry(p, c *time.Time, now time.Time) bool {
	if c != nil && !c.After(now) {
		return true
	}
	if p == nil {
		return false
	}
	return c == nil || c.After(*p)
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

func replayGrantRequest(ctx context.Context, tx *sql.Tx, table, requestID string, fp [sha256.Size]byte, conflict error) (Grant, bool, error) {
	var gid string
	var stored []byte
	err := tx.QueryRowContext(ctx, "SELECT grant_id, payload_fingerprint FROM "+table+" WHERE request_id = ?", requestID).Scan(&gid, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, false, nil
	}
	if err != nil {
		return Grant{}, false, fmt.Errorf("read grant request receipt: %w", err)
	}
	if !bytes.Equal(stored, fp[:]) {
		return Grant{}, false, conflict
	}
	g, err := grantByID(ctx, tx, gid)
	if err != nil {
		return Grant{}, false, fmt.Errorf("read grant request result: %w", err)
	}
	return g, true, nil
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
