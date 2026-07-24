package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditContextRoundTripAndPairConstraint(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	bootstrap, err := db.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	spaceID := uuid.NewString()
	memberID := uuid.NewString()
	tx, err := db.db.BeginTx(context.Background(), nil)
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
	events, err := db.ListAuditEvents(context.Background(), ListAuditEventsParams{
		OrganizationID: bootstrap.Organization.ID, AfterSequence: 3, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TargetKind != "agent" || events[0].TargetID != memberID ||
		events[0].ContextKind != "space" || events[0].ContextID != spaceID {
		t.Fatalf("audit context round trip = %+v", events)
	}
	if _, err := db.db.Exec(`
		INSERT INTO audit_events(
			id, organization_id, actor_kind, actor_id, action, target_kind, target_id,
			request_id, outcome, reason_code, occurred_at, context_kind, context_id
		) VALUES(?, ?, 'human', ?, 'space.member.remove', 'agent', ?, '', 'committed', '', ?, 'space', '')
	`, uuid.NewString(), bootstrap.Organization.ID, bootstrap.Human.ID, memberID, unixNano(now)); err == nil {
		t.Fatal("audit accepted context kind without context id")
	}
	invalidTx, err := db.db.BeginTx(context.Background(), nil)
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
