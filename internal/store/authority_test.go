package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/google/uuid"
)

func TestAuthorityBootstrapConcurrencyRestartAndCredentialMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	credential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	now := time.Date(2026, 7, 20, 12, 0, 0, 123456789, time.UTC)

	results := make([]AuthorityBootstrap, 20)
	errorsByIndex := make([]error, len(results))
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errorsByIndex[index] = database.EnsureAuthority(context.Background(), credential, now)
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("bootstrap %d: %v", index, err)
		}
		if results[index].Organization.ID != results[0].Organization.ID || results[index].Human.ID != results[0].Human.ID || results[index].RootGrant.ID != results[0].RootGrant.ID {
			t.Fatalf("bootstrap %d returned a different identity", index)
		}
	}
	events, err := database.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: results[0].Organization.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Sequence != 1 || events[2].Sequence != 3 {
		t.Fatalf("bootstrap audit events = %+v", events)
	}
	if _, err := database.EnsureAuthority(context.Background(), "different-bootstrap-credential-abcdefghijklmnopqrstuvwxyz", now); !errors.Is(err, ErrAuthorityMismatch) {
		t.Fatalf("mismatched credential error = %v", err)
	}
	var storedHash []byte
	if err := database.db.QueryRow("SELECT credential_hash FROM humans WHERE id = ?", results[0].Human.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(storedHash, []byte(credential)) || len(storedHash) != 32 {
		t.Fatalf("stored credential hash = %x", storedHash)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	restarted, err := database.EnsureAuthority(context.Background(), credential, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Organization.ID != results[0].Organization.ID || restarted.Human.ID != results[0].Human.ID || restarted.RootGrant.ID != results[0].RootGrant.ID {
		t.Fatalf("authority changed across restart: %+v", restarted)
	}
	events, err = database.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: restarted.Organization.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("restart duplicated bootstrap audit: %+v", events)
	}
}

func TestAuthorityGrantChainReceiptsAndLastOwner(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}

	secondOwner := createTestHuman(t, database, owner, "Second Owner", "owner", "second-owner-credential-abcdefghijklmnopqrstuvwxyz", now.Add(time.Second))
	member := createTestHuman(t, database, owner, "Member", "member", "member-credential-abcdefghijklmnopqrstuvwxyz-012345", now.Add(2*time.Second))
	adminGrant := issueTestGrant(t, database, IssueGrantParams{
		RequestID:     uuid.NewString(),
		Actor:         owner,
		Subject:       Principal{Kind: "human", ID: secondOwner.ID},
		Capability:    CapabilityOrganizationAdmin,
		Scope:         Scope{Kind: "organization", ID: bootstrap.Organization.ID},
		ParentGrantID: bootstrap.RootGrant.ID,
		Now:           now.Add(3 * time.Second),
	})
	secondOwnerPrincipal := Principal{Kind: "human", ID: secondOwner.ID, OrganizationID: bootstrap.Organization.ID}
	messageGrant := issueTestGrant(t, database, IssueGrantParams{
		RequestID:     uuid.NewString(),
		Actor:         secondOwnerPrincipal,
		Subject:       Principal{Kind: "human", ID: member.ID},
		Capability:    CapabilityMessageSend,
		Scope:         Scope{Kind: "organization", ID: bootstrap.Organization.ID},
		ParentGrantID: adminGrant.ID,
		Now:           now.Add(4 * time.Second),
	})
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: bootstrap.Organization.ID}
	allowed, err := database.CheckPermission(context.Background(), CheckPermissionParams{
		Subject: memberPrincipal, Capability: CapabilityMessageSend,
		Scope: Scope{Kind: "organization", ID: bootstrap.Organization.ID}, Now: now.Add(5 * time.Second),
	})
	if err != nil || !allowed {
		t.Fatalf("message permission = %v, %v", allowed, err)
	}

	revokeRequest := uuid.NewString()
	revoked, err := database.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: revokeRequest, Actor: owner, GrantID: adminGrant.ID, Now: now.Add(6 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("admin grant was not revoked")
	}
	allowed, err = database.CheckPermission(context.Background(), CheckPermissionParams{
		Subject: memberPrincipal, Capability: CapabilityMessageSend,
		Scope: Scope{Kind: "organization", ID: bootstrap.Organization.ID}, Now: now.Add(7 * time.Second),
	})
	if err != nil || allowed {
		t.Fatalf("descendant permission after parent revoke = %v, %v", allowed, err)
	}
	eventsBeforeReplay := auditCount(t, database, bootstrap.Organization.ID)
	replayed, err := database.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: revokeRequest, Actor: owner, GrantID: adminGrant.ID, Now: now.Add(8 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.UpdatedAt != revoked.UpdatedAt || auditCount(t, database, bootstrap.Organization.ID) != eventsBeforeReplay {
		t.Fatal("replayed revoke changed the grant or audit")
	}

	_, err = database.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: uuid.NewString(), Actor: owner, GrantID: bootstrap.RootGrant.ID, Now: now.Add(9 * time.Second)})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("last root revoke error = %v", err)
	}
	root, err := database.GetGrant(context.Background(), grantapp.GetQuery{GrantID: bootstrap.RootGrant.ID})
	if err != nil || root.RevokedAt != nil {
		t.Fatalf("last root changed: %+v, %v", root, err)
	}
	latest := latestAudit(t, database, bootstrap.Organization.ID)
	if latest.Outcome != "denied" || latest.ReasonCode != "last_owner" || latest.Action != AuditGrantRevoke {
		t.Fatalf("last-owner audit = %+v", latest)
	}

	updated, err := database.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: owner, HumanID: secondOwner.ID, Status: "disabled", Now: now.Add(10 * time.Second)})
	if err != nil || updated.Status != "disabled" {
		t.Fatalf("disable second owner = %+v, %v", updated, err)
	}
	_, err = database.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: owner, HumanID: bootstrap.Human.ID, Status: "disabled", Now: now.Add(11 * time.Second)})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disable last owner error = %v", err)
	}
	if messageGrant.ParentGrantID != adminGrant.ID {
		t.Fatalf("message grant parent = %q", messageGrant.ParentGrantID)
	}
}

func TestAuthorityDeniedMutationOnlyAppendsDeniedAudit(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	member := createTestHuman(t, database, owner, "No Grants", "member", "no-grants-credential-abcdefghijklmnopqrstuvwxyz-0123", now.Add(time.Second))
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: bootstrap.Organization.ID}
	humansBefore, err := database.ListHumans(context.Background(), bootstrap.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	auditBefore := auditCount(t, database, bootstrap.Organization.ID)

	_, err = database.CreateHuman(context.Background(), CreateHumanParams{
		RequestID:  uuid.NewString(),
		Actor:      memberPrincipal,
		Name:       "Denied Human",
		Role:       "member",
		Credential: "denied-human-credential-abcdefghijklmnopqrstuvwxyz",
		Now:        now.Add(2 * time.Second),
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("denied create error = %v", err)
	}
	humansAfter, err := database.ListHumans(context.Background(), bootstrap.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(humansAfter) != len(humansBefore) {
		t.Fatalf("denied create changed humans from %d to %d", len(humansBefore), len(humansAfter))
	}
	if got := auditCount(t, database, bootstrap.Organization.ID); got != auditBefore+1 {
		t.Fatalf("denied audit count = %d, want %d", got, auditBefore+1)
	}
	latest := latestAudit(t, database, bootstrap.Organization.ID)
	if latest.Actor.ID != member.ID || latest.Outcome != "denied" || latest.ReasonCode != "permission_missing" || latest.Action != AuditHumanCreate {
		t.Fatalf("denied audit = %+v", latest)
	}
}

func TestAuthorityCreateAndIssueRequestConflicts(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	requestID := uuid.NewString()
	params := CreateHumanParams{RequestID: requestID, Actor: owner, Name: "Idempotent", Role: "member", Credential: "idempotent-credential-abcdefghijklmnopqrstuvwxyz-0123", Now: now.Add(time.Second)}
	first, err := database.CreateHuman(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	params.Now = now.Add(time.Hour)
	second, err := database.CreateHuman(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || !first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("human replay changed fact: %+v / %+v", first, second)
	}
	params.Name = "Different"
	if _, err := database.CreateHuman(context.Background(), params); !errors.Is(err, ErrHumanRequestConflict) {
		t.Fatalf("human conflict error = %v", err)
	}

	grantRequest := uuid.NewString()
	grantParams := IssueGrantParams{RequestID: grantRequest, Actor: owner, Subject: Principal{Kind: "human", ID: first.ID}, Capability: CapabilitySpaceRead, Scope: Scope{Kind: "organization", ID: bootstrap.Organization.ID}, ParentGrantID: bootstrap.RootGrant.ID, Now: now.Add(2 * time.Second)}
	firstGrant, err := database.IssueGrant(context.Background(), grantParams)
	if err != nil {
		t.Fatal(err)
	}
	grantParams.Now = now.Add(time.Hour)
	secondGrant, err := database.IssueGrant(context.Background(), grantParams)
	if err != nil {
		t.Fatal(err)
	}
	if firstGrant.ID != secondGrant.ID || !firstGrant.UpdatedAt.Equal(secondGrant.UpdatedAt) {
		t.Fatalf("grant replay changed fact: %+v / %+v", firstGrant, secondGrant)
	}
	grantParams.Capability = CapabilityMessageSend
	if _, err := database.IssueGrant(context.Background(), grantParams); !errors.Is(err, ErrGrantRequestConflict) {
		t.Fatalf("grant conflict error = %v", err)
	}
}

func createTestHuman(t *testing.T, database *Store, actor Principal, name, role, credential string, now time.Time) Human {
	t.Helper()
	human, err := database.CreateHuman(context.Background(), CreateHumanParams{RequestID: uuid.NewString(), Actor: actor, Name: name, Role: role, Credential: credential, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return human
}

func issueTestGrant(t *testing.T, database *Store, params IssueGrantParams) Grant {
	t.Helper()
	grant, err := database.IssueGrant(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func auditCount(t *testing.T, database *Store, organizationID string) int {
	t.Helper()
	events, err := database.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func latestAudit(t *testing.T, database *Store, organizationID string) AuditEvent {
	t.Helper()
	events, err := database.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no audit events")
	}
	return events[len(events)-1]
}
