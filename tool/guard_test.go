package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/command"
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

func TestEnsureWritePathAllowsDotPrefixedNamesInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "..note")
	if err := ensureWritePath(dir, path); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureWritePathRejectsParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "..", "note")
	if err := ensureWritePath(dir, path); err == nil {
		t.Fatal("expected outside workspace error")
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

func TestRunContextTelegramBlocksShellTools(t *testing.T) {
	r := NewRegistry(t.TempDir())
	ctx := command.WithRunContext(context.Background(), command.RunContext{Permission: "telegram"})
	out, err := r.Run(ctx, "bash", json.RawMessage(`{"cmd":"printf hi"}`))
	if err == nil {
		t.Fatalf("expected permission error, output=%q", out)
	}
	if got := err.Error(); got == "" || !containsAll(got, "permission denied", "notify_bark") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunContextCronBlocksShellNetworkCommands(t *testing.T) {
	r := NewRegistry(t.TempDir())
	ctx := command.WithRunContext(context.Background(), command.RunContext{Permission: "cron"})
	out, err := r.Run(ctx, "bash", json.RawMessage(`{"cmd":"curl https://api.day.app/key/title/body"}`))
	if err == nil {
		t.Fatalf("expected permission error, output=%q", out)
	}
	if got := err.Error(); got == "" || !containsAll(got, "permission denied", "notify_bark") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunContextCronAllowsLocalShellCommands(t *testing.T) {
	r := NewRegistry(t.TempDir())
	ctx := command.WithRunContext(context.Background(), command.RunContext{Permission: "cron"})
	out, err := r.Run(ctx, "bash", json.RawMessage(`{"cmd":"printf ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Fatalf("output = %q, want ok", out)
	}
}

func TestRunContextCronAllowsNonWebhookShellCommands(t *testing.T) {
	r := NewRegistry(t.TempDir())
	ctx := command.WithRunContext(context.Background(), command.RunContext{Permission: "cron"})
	out, err := r.Run(ctx, "bash", json.RawMessage(`{"cmd":"printf '{\"ok\":true}'"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"ok":true}` {
		t.Fatalf("output = %q", out)
	}
}

func TestIsNetworkCommand(t *testing.T) {
	for _, cmd := range []string{
		"curl https://example.com",
		"wget http://example.com",
		"bash -lc 'curl https://example.com'",
		"python3 -c 'import requests; requests.post(\"https://example.com\")'",
	} {
		if !IsNetworkCommand(cmd) {
			t.Fatalf("IsNetworkCommand(%q) = false, want true", cmd)
		}
	}
	if IsNetworkCommand("printf ok") {
		t.Fatal("printf should not be network command")
	}
}

func TestIsWebhookCommand(t *testing.T) {
	for _, cmd := range []string{
		"curl https://api.day.app/key/title/body",
		"curl -X POST https://example.com/notify",
		"bash -lc 'curl https://example.com/webhook'",
	} {
		if !IsWebhookCommand(cmd) {
			t.Fatalf("IsWebhookCommand(%q) = false, want true", cmd)
		}
	}
	if IsWebhookCommand("curl https://example.com/state.json") {
		t.Fatal("read-only state fetch should not be treated as webhook")
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

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}
