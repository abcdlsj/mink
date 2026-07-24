package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/google/uuid"
)

func TestBootstrapLocalAccountIsOneShotAndIssuesExistingBrowserSession(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	bootstrap, err := db.EnsureAuthority(ctx, "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	required, err := db.LocalAccountSetupRequired(ctx)
	if err != nil || !required {
		t.Fatalf("initial setup required = %v, %v", required, err)
	}

	command := authorityapp.BindBootstrapLocalAccountCommand{
		RequestID:      uuid.NewString(),
		BootstrapHuman: Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID},
		Identity:       authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password: authorityapp.PasswordDigest{
			Algorithm: "argon2id", Salt: []byte("0123456789abcdef"), Digest: make([]byte, 32),
			Memory: 8192, Iterations: 1, Parallelism: 1,
		},
		SessionToken: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Now: now, SessionExpiresAt: now.Add(time.Hour),
	}
	principal, err := db.BindBootstrapLocalAccount(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if principal.ID != bootstrap.Human.ID || principal.OrganizationID != bootstrap.Organization.ID {
		t.Fatalf("principal = %+v", principal)
	}
	required, err = db.LocalAccountSetupRequired(ctx)
	if err != nil || required {
		t.Fatalf("completed setup required = %v, %v", required, err)
	}
	account, err := db.GetLocalAccount(ctx, "owner")
	if err != nil || account.Human != principal || account.Identity.Subject != "owner" {
		t.Fatalf("account = %+v, %v", account, err)
	}
	session, err := db.AuthenticateBrowserSession(ctx, command.SessionToken, now.Add(time.Minute))
	if err != nil || session != principal {
		t.Fatalf("session = %+v, %v", session, err)
	}

	command.RequestID = uuid.NewString()
	command.Identity.Subject = "second"
	command.SessionToken = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	if _, err := db.BindBootstrapLocalAccount(ctx, command); !errors.Is(err, ErrLocalAccountSetupDone) {
		t.Fatalf("second setup error = %v", err)
	}
	var identities, sessions, audits int
	if err := db.db.QueryRow(`SELECT count(*) FROM auth_identities WHERE provider = 'local'`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM browser_sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = ?`, AuditAuthIdentityBind).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if identities != 1 || sessions != 1 || audits != 1 {
		t.Fatalf("identities/sessions/audits = %d/%d/%d", identities, sessions, audits)
	}
}

func TestLocalAccountAndSessionFailClosedWhenHumanIsDisabled(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	bootstrap, err := db.EnsureAuthority(ctx, "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	token := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err = db.BindBootstrapLocalAccount(ctx, authorityapp.BindBootstrapLocalAccountCommand{
		RequestID:      uuid.NewString(),
		BootstrapHuman: Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID},
		Identity:       authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password: authorityapp.PasswordDigest{
			Algorithm: "argon2id", Salt: []byte("0123456789abcdef"), Digest: make([]byte, 32),
			Memory: 8192, Iterations: 1, Parallelism: 1,
		},
		SessionToken: token, Now: now, SessionExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE humans SET status = 'disabled' WHERE id = ?`, bootstrap.Human.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetLocalAccount(ctx, "owner"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled local account error = %v", err)
	}
	if _, err := db.AuthenticateBrowserSession(ctx, token, now.Add(time.Minute)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled session error = %v", err)
	}
	if err := db.CreateBrowserSession(ctx, authorityapp.CreateBrowserSessionCommand{
		Human: Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID},
		Token: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", Now: now, ExpiresAt: now.Add(time.Hour),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled session creation error = %v", err)
	}
}
