package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestOpenUpgradesVersionSixAuditWithoutContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(database); err != nil {
		database.Close()
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 6); err != nil {
		database.Close()
		t.Fatal(err)
	}
	organizationID := uuid.NewString()
	humanID := uuid.NewString()
	grantID := uuid.NewString()
	now := time.Now()
	stamp := unixNano(now)
	credentialHash := sha256.Sum256([]byte("legacy-bootstrap-credential"))
	if _, err := database.Exec(`
		INSERT INTO organizations(singleton, id, name, bootstrap_human_id, created_at)
		VALUES(1, ?, 'Sumi', ?, ?)
	`, organizationID, humanID, stamp); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO humans(id, organization_id, name, role, status, credential_hash, created_at, updated_at)
		VALUES(?, ?, 'Owner', 'owner', 'active', ?, ?, ?)
	`, humanID, organizationID, credentialHash[:], stamp, stamp); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO grants(
			id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
			capability, scope_kind, scope_id, parent_grant_id, created_at, updated_at
		)
		VALUES(?, ?, 'human', ?, 'system', '', ?, 'organization', ?, '', ?, ?)
	`, grantID, organizationID, humanID, CapabilityOrganizationAdmin, organizationID, stamp, stamp); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO audit_events(
			id, organization_id, actor_kind, actor_id, action, target_kind,
			target_id, request_id, outcome, reason_code, occurred_at
		) VALUES(?, ?, 'system', '', 'organization.bootstrap', 'organization', ?, '', 'committed', '', ?)
	`, uuid.NewString(), organizationID, organizationID, stamp); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	events, err := current.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ContextKind != "" || event.ContextID != "" {
			t.Fatalf("legacy audit context = %q/%q", event.ContextKind, event.ContextID)
		}
	}
}

func TestAuditContextRoundTripAndPairConstraint(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now()
	bootstrap, err := database.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	spaceID := uuid.NewString()
	memberID := uuid.NewString()
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendAuditEvent(context.Background(), tx, AppendAuditParams{
		OrganizationID: bootstrap.Organization.ID,
		Actor:          Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID},
		Action:         AuditSpaceMemberRemove,
		TargetKind:     "agent",
		TargetID:       memberID,
		ContextKind:    "space",
		ContextID:      spaceID,
		RequestID:      uuid.NewString(),
		Outcome:        "committed",
		Now:            now,
	}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	events, err := database.ListAuditEvents(context.Background(), ListAuditEventsParams{
		OrganizationID: bootstrap.Organization.ID, AfterSequence: 3, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TargetKind != "agent" || events[0].TargetID != memberID ||
		events[0].ContextKind != "space" || events[0].ContextID != spaceID {
		t.Fatalf("audit context round trip = %+v", events)
	}
	if _, err := database.db.Exec(`
		INSERT INTO audit_events(
			id, organization_id, actor_kind, actor_id, action, target_kind, target_id,
			request_id, outcome, reason_code, occurred_at, context_kind, context_id
		) VALUES(?, ?, 'human', ?, 'space.member.remove', 'agent', ?, '', 'committed', '', ?, 'space', '')
	`, uuid.NewString(), bootstrap.Organization.ID, bootstrap.Human.ID, memberID, unixNano(now)); err == nil {
		t.Fatal("audit accepted context kind without context id")
	}
	invalidTx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendAuditEvent(context.Background(), invalidTx, AppendAuditParams{
		OrganizationID: bootstrap.Organization.ID,
		Actor:          Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID},
		Action:         AuditMessageSend,
		TargetKind:     "message",
		TargetID:       uuid.NewString(),
		ContextKind:    "space",
		ContextID:      "00000000-0000-0000-0000-00000000000A",
		Outcome:        "committed",
		Now:            now,
	}); err == nil {
		invalidTx.Rollback()
		t.Fatal("audit accepted non-canonical context id")
	}
	invalidTx.Rollback()
}
