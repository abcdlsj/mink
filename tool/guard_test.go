package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRegistryGuardCanCancelToolRun(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	r.SetGuard(guardFunc(func(ctx context.Context, call Call) (bool, error) {
		if call.Tool != "bash" {
			t.Fatalf("unexpected tool %q", call.Tool)
		}
		return false, nil
	}))

	out, err := r.Run(context.Background(), "bash", json.RawMessage(`{"cmd":"printf hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "cancelled" {
		t.Fatalf("output = %q, want %q", out, "cancelled")
	}
}

func TestRegistryGuardReceivesResolvedPath(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	want := filepath.Join(dir, "note.txt")
	var got string
	r.SetGuard(guardFunc(func(ctx context.Context, call Call) (bool, error) {
		got = call.Action
		return true, nil
	}))

	if _, err := r.Run(context.Background(), "write", json.RawMessage(`{"path":"note.txt","content":"hi"}`)); err != nil {
		t.Fatal(err)
	}
	if got != "write "+want {
		t.Fatalf("action = %q, want %q", got, "write "+want)
	}
}

func TestPolicyGuardAllowAlwaysPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.json")
	g := NewPolicyGuard("", path)
	calls := 0
	g.SetApprover(approverFunc(func(ctx context.Context, req Request) (Approval, error) {
		calls++
		return AllowAlways, nil
	}))

	call := Call{Tool: "bash", Action: "bash git push origin main"}
	ok, err := g.Allow(context.Background(), call)
	if err != nil || !ok {
		t.Fatalf("allow = %v, err = %v", ok, err)
	}
	ok, err = g.Allow(context.Background(), call)
	if err != nil || !ok {
		t.Fatalf("allow = %v, err = %v", ok, err)
	}
	if calls != 1 {
		t.Fatalf("approver calls = %d, want 1", calls)
	}
}

func TestPolicyGuardDeniesWithoutApprover(t *testing.T) {
	g := NewPolicyGuard("", filepath.Join(t.TempDir(), "permissions.json"))
	ok, err := g.Allow(context.Background(), Call{Tool: "bash", Action: "bash git push origin main"})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected deny")
	}
}

type guardFunc func(context.Context, Call) (bool, error)

func (f guardFunc) Allow(ctx context.Context, call Call) (bool, error) {
	return f(ctx, call)
}

type approverFunc func(context.Context, Request) (Approval, error)

func (f approverFunc) Approve(ctx context.Context, req Request) (Approval, error) {
	return f(ctx, req)
}
