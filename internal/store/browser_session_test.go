package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBrowserHandoffIsSingleUseAndSessionSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	bootstrap, err := db.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	handoff := browserTestToken(250)
	if err := db.CreateBrowserHandoff(context.Background(), CreateBrowserHandoffParams{
		Human: owner, Token: handoff, Now: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	tokens := make([]string, 20)
	principals := make([]Principal, len(tokens))
	errorsByIndex := make([]error, len(tokens))
	var wait sync.WaitGroup
	for index := range tokens {
		tokens[index] = browserTestToken(byte(index + 1))
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			principals[index], errorsByIndex[index] = db.ConsumeBrowserHandoff(context.Background(), ConsumeBrowserHandoffParams{
				HandoffToken: handoff, SessionToken: tokens[index], Now: now.Add(time.Second), SessionExpiresAt: now.Add(12 * time.Hour),
			})
		}(index)
	}
	wait.Wait()
	winner := -1
	for index, err := range errorsByIndex {
		if err == nil {
			if winner != -1 {
				t.Fatalf("handoff consumed by %d and %d", winner, index)
			}
			winner = index
			if principals[index] != owner {
				t.Fatalf("principal = %+v, want %+v", principals[index], owner)
			}
			continue
		}
		if !errors.Is(err, ErrBrowserHandoffInvalid) {
			t.Fatalf("consume %d error = %v", index, err)
		}
	}
	if winner == -1 {
		t.Fatal("handoff was not consumed")
	}
	if _, err := db.AuthenticateBrowserSession(context.Background(), tokens[winner], now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	for index, token := range tokens {
		if index == winner {
			continue
		}
		if _, err := db.AuthenticateBrowserSession(context.Background(), token, now.Add(2*time.Second)); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("losing token %d authenticated: %v", index, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	principal, err := db.AuthenticateBrowserSession(context.Background(), tokens[winner], now.Add(time.Hour))
	if err != nil || principal != owner {
		t.Fatalf("restarted session = %+v, %v", principal, err)
	}
	if err := db.RevokeBrowserSession(context.Background(), tokens[winner], now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AuthenticateBrowserSession(context.Background(), tokens[winner], now.Add(2*time.Hour)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked session error = %v", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range append(tokens, handoff) {
		if bytes.Contains(payload, []byte(raw)) {
			t.Fatalf("raw browser token found in db")
		}
	}
}

func TestBrowserSessionRejectsExpiredAndDisabledHuman(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	bootstrap, err := db.EnsureAuthority(context.Background(), "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	member := createTestHuman(t, db, owner, "Browser Member", "member", "browser-member-credential-abcdefghijklmnopqrstuvwxyz", now.Add(time.Second))
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: owner.OrganizationID}

	expiredHandoff := browserTestToken(249)
	if err := db.CreateBrowserHandoff(context.Background(), CreateBrowserHandoffParams{
		Human: memberPrincipal, Token: expiredHandoff, Now: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = db.ConsumeBrowserHandoff(context.Background(), ConsumeBrowserHandoffParams{
		HandoffToken: expiredHandoff, SessionToken: browserTestToken(248), Now: now.Add(time.Minute), SessionExpiresAt: now.Add(time.Hour),
	})
	if !errors.Is(err, ErrBrowserHandoffInvalid) {
		t.Fatalf("expired handoff error = %v", err)
	}
	expiringHandoff := browserTestToken(244)
	if err := db.CreateBrowserHandoff(context.Background(), CreateBrowserHandoffParams{
		Human: owner, Token: expiringHandoff, Now: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	expiringSession := browserTestToken(243)
	if _, err := db.ConsumeBrowserHandoff(context.Background(), ConsumeBrowserHandoffParams{
		HandoffToken: expiringHandoff, SessionToken: expiringSession, Now: now.Add(time.Second), SessionExpiresAt: now.Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AuthenticateBrowserSession(context.Background(), expiringSession, now.Add(10*time.Second)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expired session error = %v", err)
	}

	handoff := browserTestToken(247)
	if err := db.CreateBrowserHandoff(context.Background(), CreateBrowserHandoffParams{
		Human: memberPrincipal, Token: handoff, Now: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	session := browserTestToken(246)
	if _, err := db.ConsumeBrowserHandoff(context.Background(), ConsumeBrowserHandoffParams{
		HandoffToken: handoff, SessionToken: session, Now: now.Add(2 * time.Second), SessionExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetHumanStatus(context.Background(), SetHumanStatusParams{
		RequestID: uuid.NewString(), Actor: owner, HumanID: member.ID, Status: "disabled", Now: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AuthenticateBrowserSession(context.Background(), session, now.Add(4*time.Second)); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled human session error = %v", err)
	}
	if err := db.CreateBrowserHandoff(context.Background(), CreateBrowserHandoffParams{
		Human: memberPrincipal, Token: browserTestToken(245), Now: now.Add(4 * time.Second), ExpiresAt: now.Add(time.Minute),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled human handoff error = %v", err)
	}
}

func browserTestToken(fill byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}
