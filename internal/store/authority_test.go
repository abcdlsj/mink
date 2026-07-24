package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/google/uuid"
)

func TestAuthorityBootstrapConcurrencyRestartAndCredentialMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := Open(path)
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
			results[index], errorsByIndex[index] = db.EnsureAuthority(context.Background(), credential, now)
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
	events, err := db.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: results[0].Organization.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Sequence != 1 || events[2].Sequence != 3 {
		t.Fatalf("bootstrap audit events = %+v", events)
	}
	if _, err := db.EnsureAuthority(context.Background(), "different-bootstrap-credential-abcdefghijklmnopqrstuvwxyz", now); !errors.Is(err, ErrAuthorityMismatch) {
		t.Fatalf("mismatched credential error = %v", err)
	}
	var storedHash []byte
	if err := db.db.QueryRow("SELECT credential_hash FROM humans WHERE id = ?", results[0].Human.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(storedHash, []byte(credential)) || len(storedHash) != 32 {
		t.Fatalf("stored credential hash = %x", storedHash)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restarted, err := db.EnsureAuthority(context.Background(), credential, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Organization.ID != results[0].Organization.ID || restarted.Human.ID != results[0].Human.ID || restarted.RootGrant.ID != results[0].RootGrant.ID {
		t.Fatalf("authority changed across restart: %+v", restarted)
	}
	events, err = db.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: restarted.Organization.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("restart duplicated bootstrap audit: %+v", events)
	}
}

func TestAuthorityGrantChainReceiptsAndLastOwner(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	bootstrap, err := db.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}

	secondOwner := createTestHuman(t, db, owner, "Second Owner", "owner", "second-owner-credential-abcdefghijklmnopqrstuvwxyz", now.Add(time.Second))
	member := createTestHuman(t, db, owner, "Member", "member", "member-credential-abcdefghijklmnopqrstuvwxyz-012345", now.Add(2*time.Second))
	adminGrant := issueTestGrant(t, db, IssueGrantParams{
		RequestID:     uuid.NewString(),
		Actor:         owner,
		Subject:       Principal{Kind: "human", ID: secondOwner.ID},
		Capability:    CapabilityOrganizationAdmin,
		Scope:         Scope{Kind: "organization", ID: bootstrap.Organization.ID},
		ParentGrantID: bootstrap.RootGrant.ID,
		Now:           now.Add(3 * time.Second),
	})
	secondOwnerPrincipal := Principal{Kind: "human", ID: secondOwner.ID, OrganizationID: bootstrap.Organization.ID}
	messageGrant := issueTestGrant(t, db, IssueGrantParams{
		RequestID:     uuid.NewString(),
		Actor:         secondOwnerPrincipal,
		Subject:       Principal{Kind: "human", ID: member.ID},
		Capability:    CapabilityMessageSend,
		Scope:         Scope{Kind: "organization", ID: bootstrap.Organization.ID},
		ParentGrantID: adminGrant.ID,
		Now:           now.Add(4 * time.Second),
	})
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: bootstrap.Organization.ID}
	allowed, err := db.CheckPermission(context.Background(), CheckPermissionParams{
		Subject: memberPrincipal, Capability: CapabilityMessageSend,
		Scope: Scope{Kind: "organization", ID: bootstrap.Organization.ID}, Now: now.Add(5 * time.Second),
	})
	if err != nil || !allowed {
		t.Fatalf("message permission = %v, %v", allowed, err)
	}

	revokeRequest := uuid.NewString()
	revoked, err := db.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: revokeRequest, Actor: owner, GrantID: adminGrant.ID, Now: now.Add(6 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("admin grant was not revoked")
	}
	allowed, err = db.CheckPermission(context.Background(), CheckPermissionParams{
		Subject: memberPrincipal, Capability: CapabilityMessageSend,
		Scope: Scope{Kind: "organization", ID: bootstrap.Organization.ID}, Now: now.Add(7 * time.Second),
	})
	if err != nil || allowed {
		t.Fatalf("descendant permission after parent revoke = %v, %v", allowed, err)
	}
	eventsBeforeReplay := auditCount(t, db, bootstrap.Organization.ID)
	replayed, err := db.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: revokeRequest, Actor: owner, GrantID: adminGrant.ID, Now: now.Add(8 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.UpdatedAt != revoked.UpdatedAt || auditCount(t, db, bootstrap.Organization.ID) != eventsBeforeReplay {
		t.Fatal("replayed revoke changed the grant or audit")
	}

	_, err = db.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: uuid.NewString(), Actor: owner, GrantID: bootstrap.RootGrant.ID, Now: now.Add(9 * time.Second)})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("last root revoke error = %v", err)
	}
	root, err := db.GetGrant(context.Background(), grantapp.GetQuery{GrantID: bootstrap.RootGrant.ID})
	if err != nil || root.RevokedAt != nil {
		t.Fatalf("last root changed: %+v, %v", root, err)
	}
	latest := latestAudit(t, db, bootstrap.Organization.ID)
	if latest.Outcome != "denied" || latest.ReasonCode != "last_owner" || latest.Action != AuditGrantRevoke {
		t.Fatalf("last-owner audit = %+v", latest)
	}

	updated, err := db.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: owner, HumanID: secondOwner.ID, Status: "disabled", Now: now.Add(10 * time.Second)})
	if err != nil || updated.Status != "disabled" {
		t.Fatalf("disable second owner = %+v, %v", updated, err)
	}
	_, err = db.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: owner, HumanID: bootstrap.Human.ID, Status: "disabled", Now: now.Add(11 * time.Second)})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disable last owner error = %v", err)
	}
	if messageGrant.ParentGrantID != adminGrant.ID {
		t.Fatalf("message grant parent = %q", messageGrant.ParentGrantID)
	}
}

func TestAuthorityRecoverableOwnerIterationErrorsFailClosed(t *testing.T) {
	for _, operation := range []string{"revoke grant", "disable human"} {
		t.Run(operation, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "server.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
			bootstrap, err := db.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
			if err != nil {
				t.Fatal(err)
			}
			owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
			target := createTestHuman(t, db, owner, "Target Owner", "owner", "target-owner-credential-abcdefghijklmnopqrstuvwxyz", now.Add(time.Second))
			createTestHuman(t, db, owner, "Spare Owner", "owner", "spare-owner-credential-abcdefghijklmnopqrstuvwxyz", now.Add(2*time.Second))
			targetGrant := issueTestGrant(t, db, IssueGrantParams{
				RequestID: uuid.NewString(), Actor: owner,
				Subject: Principal{Kind: "human", ID: target.ID}, Capability: CapabilityOrganizationAdmin,
				Scope: Scope{Kind: "organization", ID: bootstrap.Organization.ID}, ParentGrantID: bootstrap.RootGrant.ID,
				Now: now.Add(3 * time.Second),
			})
			auditsBefore := auditCount(t, db, bootstrap.Organization.ID)
			ctx, probe, cancel := recoverableOwnerCancellationContext()
			defer cancel()
			requestID := uuid.NewString()

			switch operation {
			case "revoke grant":
				_, err = db.RevokeGrant(ctx, RevokeGrantParams{
					RequestID: requestID, Actor: owner, GrantID: targetGrant.ID, Now: now.Add(4 * time.Second),
				})
			case "disable human":
				_, err = db.SetHumanStatus(ctx, SetHumanStatusParams{
					RequestID: requestID, Actor: owner, HumanID: target.ID, Status: "disabled", Now: now.Add(4 * time.Second),
				})
			}
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "iterate recoverable owners") {
				t.Fatalf("%s iteration error = %v", operation, err)
			}
			if probe.scanned != 1 || !probe.cancellationObserved {
				t.Fatalf("%s probe = %+v", operation, probe)
			}
			if got := auditCount(t, db, bootstrap.Organization.ID); got != auditsBefore {
				t.Fatalf("%s audit count = %d, want %d", operation, got, auditsBefore)
			}

			var receipts int
			switch operation {
			case "revoke grant":
				grant, getErr := db.GetGrant(context.Background(), grantapp.GetQuery{GrantID: targetGrant.ID})
				if getErr != nil || grant.RevokedAt != nil {
					t.Fatalf("grant changed after iteration error: %+v, %v", grant, getErr)
				}
				if err := db.db.QueryRow(`SELECT count(*) FROM grant_revoke_requests WHERE request_id = ?`, requestID).Scan(&receipts); err != nil {
					t.Fatal(err)
				}
			case "disable human":
				var status string
				if err := db.db.QueryRow(`SELECT status FROM humans WHERE id = ?`, target.ID).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "active" {
					t.Fatalf("human status after iteration error = %q", status)
				}
				if err := db.db.QueryRow(`SELECT count(*) FROM human_status_requests WHERE request_id = ?`, requestID).Scan(&receipts); err != nil {
					t.Fatal(err)
				}
			}
			if receipts != 0 {
				t.Fatalf("%s request receipts = %d", operation, receipts)
			}
		})
	}
}

type recoverableOwnerCancellationProbe struct {
	scanned              int
	cancellationObserved bool
}

func recoverableOwnerCancellationContext() (context.Context, *recoverableOwnerCancellationProbe, context.CancelFunc) {
	queryContext, cancel := context.WithCancel(context.Background())
	probe := &recoverableOwnerCancellationProbe{}
	ctx := context.WithValue(queryContext, ownerAfterScanKey{}, ownerAfterScanFn(func(scanned int, rows *sql.Rows) {
		probe.scanned = scanned
		if scanned == 1 {
			cancel()
			deadline := time.Now().Add(time.Second)
			for rows.Err() == nil && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			probe.cancellationObserved = errors.Is(rows.Err(), context.Canceled)
		}
	}))
	return ctx, probe, cancel
}

func TestAuthorityDeniedMutationOnlyAppendsDeniedAudit(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	bootstrap, err := db.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	member := createTestHuman(t, db, owner, "No Grants", "member", "no-grants-credential-abcdefghijklmnopqrstuvwxyz-0123", now.Add(time.Second))
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: bootstrap.Organization.ID}
	humansBefore, err := db.ListHumans(context.Background(), bootstrap.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	auditBefore := auditCount(t, db, bootstrap.Organization.ID)

	_, err = db.CreateHuman(context.Background(), CreateHumanParams{
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
	humansAfter, err := db.ListHumans(context.Background(), bootstrap.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(humansAfter) != len(humansBefore) {
		t.Fatalf("denied create changed humans from %d to %d", len(humansBefore), len(humansAfter))
	}
	if got := auditCount(t, db, bootstrap.Organization.ID); got != auditBefore+1 {
		t.Fatalf("denied audit count = %d, want %d", got, auditBefore+1)
	}
	latest := latestAudit(t, db, bootstrap.Organization.ID)
	if latest.Actor.ID != member.ID || latest.Outcome != "denied" || latest.ReasonCode != "permission_missing" || latest.Action != AuditHumanCreate {
		t.Fatalf("denied audit = %+v", latest)
	}
}

func TestAuthorityCreateAndIssueRequestConflicts(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	bootstrap, err := db.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	requestID := uuid.NewString()
	params := CreateHumanParams{RequestID: requestID, Actor: owner, Name: "Idempotent", Role: "member", Credential: "idempotent-credential-abcdefghijklmnopqrstuvwxyz-0123", Now: now.Add(time.Second)}
	first, err := db.CreateHuman(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	params.Now = now.Add(time.Hour)
	second, err := db.CreateHuman(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || !first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("human replay changed fact: %+v / %+v", first, second)
	}
	params.Name = "Different"
	if _, err := db.CreateHuman(context.Background(), params); !errors.Is(err, ErrHumanRequestConflict) {
		t.Fatalf("human conflict error = %v", err)
	}

	grantRequest := uuid.NewString()
	grantParams := IssueGrantParams{RequestID: grantRequest, Actor: owner, Subject: Principal{Kind: "human", ID: first.ID}, Capability: CapabilitySpaceRead, Scope: Scope{Kind: "organization", ID: bootstrap.Organization.ID}, ParentGrantID: bootstrap.RootGrant.ID, Now: now.Add(2 * time.Second)}
	firstGrant, err := db.IssueGrant(context.Background(), grantParams)
	if err != nil {
		t.Fatal(err)
	}
	grantParams.Now = now.Add(time.Hour)
	secondGrant, err := db.IssueGrant(context.Background(), grantParams)
	if err != nil {
		t.Fatal(err)
	}
	if firstGrant.ID != secondGrant.ID || !firstGrant.UpdatedAt.Equal(secondGrant.UpdatedAt) {
		t.Fatalf("grant replay changed fact: %+v / %+v", firstGrant, secondGrant)
	}
	grantParams.Capability = CapabilityMessageSend
	if _, err := db.IssueGrant(context.Background(), grantParams); !errors.Is(err, ErrGrantRequestConflict) {
		t.Fatalf("grant conflict error = %v", err)
	}
}

func createTestHuman(t *testing.T, db *Store, actor Principal, name, role, credential string, now time.Time) Human {
	t.Helper()
	human, err := db.CreateHuman(context.Background(), CreateHumanParams{RequestID: uuid.NewString(), Actor: actor, Name: name, Role: role, Credential: credential, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return human
}

func issueTestGrant(t *testing.T, db *Store, params IssueGrantParams) Grant {
	t.Helper()
	grant, err := db.IssueGrant(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func auditCount(t *testing.T, db *Store, organizationID string) int {
	t.Helper()
	events, err := db.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func latestAudit(t *testing.T, db *Store, organizationID string) AuditEvent {
	t.Helper()
	events, err := db.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no audit events")
	}
	return events[len(events)-1]
}
