package sessioncmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
)

// contractApp builds an App with the sessioncmd plugin loaded and routes input
// through the real command router, so these tests exercise the exact command
// implementation an end user hits (whichever registration wins), not a
// hand-picked struct. This is the Phase 1 "single-agent direct" contract: the
// /session verbs a user can rely on must keep working through dispatch.
func contractApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := Plugin()(a); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})
	return a
}

func route(t *testing.T, a *app.App, source, input string) string {
	t.Helper()
	out, err := a.HandleInput(command.WithSource(context.Background(), source), source, input)
	if err != nil {
		t.Fatalf("route %q: %v", input, err)
	}
	return out
}

// TestSessionContractSupersetVerbs locks the current user-visible /session
// surface: the effective implementation is the sessioncmd superset, which
// offers list/current/new/switch PLUS fork and close. Phase 1 dedup removes the
// shadowed core registration; this test proves no verb regresses.
func TestSessionContractSupersetVerbs(t *testing.T) {
	a := contractApp(t)
	source := "cli:direct:contract"

	// Seed a real turn so the current session is non-empty: List() filters out
	// empty sessions, so an unseeded session never appears in /session list. This
	// mirrors what a user actually sees after conversing.
	route(t, a, source, "hello there")

	// current shows the superset format (Current session / Entries count).
	cur := route(t, a, source, "/session current")
	if !strings.Contains(cur, "Current session:") || !strings.Contains(cur, "Entries:") {
		t.Fatalf("current must show the superset format (Current session/Entries), got:\n%s", cur)
	}

	// list shows a header and marks the current session with '*' — superset-only
	// formatting the core impl never produced.
	list := route(t, a, source, "/session list")
	if !strings.HasPrefix(list, "Sessions:") {
		t.Fatalf("list must start with the superset 'Sessions:' header, got:\n%s", list)
	}
	if !strings.Contains(list, "* ") {
		t.Fatalf("list must mark the current session with '* ', got:\n%s", list)
	}

	// fork is a superset-only verb: it must succeed and report a new id.
	if out := route(t, a, source, "/session fork"); !strings.HasPrefix(out, "forked current session: ") {
		t.Fatalf("fork must be supported (superset verb), got: %q", out)
	}

	// close is a superset-only verb: it must succeed and switch to a new session.
	if out := route(t, a, source, "/session close"); !strings.Contains(out, "closed current session") {
		t.Fatalf("close must be supported (superset verb), got: %q", out)
	}

	// Unknown subcommand returns the superset usage string (lists fork|close).
	usage := route(t, a, source, "/session bogus")
	if !strings.Contains(usage, "fork") || !strings.Contains(usage, "close") {
		t.Fatalf("usage must advertise superset verbs fork|close, got: %q", usage)
	}
}
