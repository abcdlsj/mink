package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWorkCursorSealsBindsAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: uuid.NewString()}
	binding := WorkCursorBinding{PrincipalFingerprint: workCursorPrincipalFingerprint(principal), OrganizationID: principal.OrganizationID}
	seek := WorkCursorSeekKey{RootWorkID: uuid.NewString(), ParentIsNull: true, CreatedAt: time.Date(2026, time.July, 23, 8, 0, 0, 123, time.UTC), ID: uuid.NewString()}
	cursor, err := first.SealWorkCursor(binding, seek)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cursor, seek.RootWorkID) || strings.Contains(cursor, seek.ID) {
		t.Fatal("sealed work cursor exposed a work identifier")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.OpenWorkCursor(cursor, binding)
	if err != nil || got != seek {
		t.Fatalf("reopened work cursor = %+v, %v", got, err)
	}
	if _, err := second.OpenWorkCursor(cursor, WorkCursorBinding{PrincipalFingerprint: workCursorPrincipalFingerprint(Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: principal.OrganizationID}), OrganizationID: principal.OrganizationID}); !errors.Is(err, ErrWorkCursorUnavailable) {
		t.Fatalf("principal-mismatched cursor error = %v", err)
	}
	if _, err := second.OpenWorkCursor(cursor, WorkCursorBinding{PrincipalFingerprint: binding.PrincipalFingerprint, OrganizationID: uuid.NewString()}); !errors.Is(err, ErrWorkCursorUnavailable) {
		t.Fatalf("organization-mismatched cursor error = %v", err)
	}
	if _, err := second.OpenKnowledgeCursor(cursor, KnowledgeCursorBinding{}); !errors.Is(err, ErrKnowledgeCursorUnavailable) {
		t.Fatalf("work cursor opened as knowledge cursor: %v", err)
	}
}

func TestWorkCursorRejectsTamperAndOversize(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	principal := Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: uuid.NewString()}
	binding := WorkCursorBinding{PrincipalFingerprint: workCursorPrincipalFingerprint(principal), OrganizationID: principal.OrganizationID}
	seek := WorkCursorSeekKey{RootWorkID: uuid.NewString(), ParentWorkID: uuid.NewString(), CreatedAt: time.Now().UTC(), ID: uuid.NewString()}
	cursor, err := database.SealWorkCursor(binding, seek)
	if err != nil {
		t.Fatal(err)
	}
	index := len(cursor) / 2
	replacement := byte('A')
	if cursor[index] == replacement {
		replacement = 'B'
	}
	mutated := cursor[:index] + string(replacement) + cursor[index+1:]
	if _, err := database.OpenWorkCursor(mutated, binding); !errors.Is(err, ErrWorkCursorUnavailable) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	if _, err := database.OpenWorkCursor(strings.Repeat("a", 2049), binding); !errors.Is(err, ErrWorkCursorUnavailable) {
		t.Fatalf("oversized cursor error = %v", err)
	}
}

func TestWorkCursorRejectsInvalidTuple(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	bootstrap, err := database.EnsureAuthority(context.Background(), "work-cursor-bootstrap-credential-abcdefghijklmnopqrstuvwxyz", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	binding := WorkCursorBinding{PrincipalFingerprint: workCursorPrincipalFingerprint(principal), OrganizationID: principal.OrganizationID}
	if _, err := database.SealWorkCursor(binding, WorkCursorSeekKey{RootWorkID: "not-a-uuid", ParentIsNull: true, CreatedAt: time.Now(), ID: uuid.NewString()}); !errors.Is(err, ErrWorkCursorUnavailable) {
		t.Fatalf("invalid tuple seal error = %v", err)
	}
}
