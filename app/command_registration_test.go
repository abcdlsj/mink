package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
)

func regTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "native",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestRegisterCommandDuplicateSurfacesError proves the fail-fast propagates all
// the way to a plugin: a plugin that registers a name already owned by a core
// builtin gets a hard error from Use, instead of silently shadowing the core
// command by load order. This is the mechanism that makes the /session and
// /compact dedup enforceable rather than convention.
func TestRegisterCommandDuplicateSurfacesError(t *testing.T) {
	a := regTestApp(t)

	// "compact" is a core builtin; re-registering it must fail.
	dupPlugin := func(app *App) error {
		return app.RegisterCommand(command.NewFuncCmd("compact", "dup", func(ctx context.Context, args []string) (string, error) {
			return "", nil
		}))
	}

	err := a.Use(dupPlugin)
	if err == nil {
		t.Fatalf("registering a duplicate command name via a plugin must error")
	}
	if !strings.Contains(err.Error(), "compact") {
		t.Fatalf("error should name the colliding command, got %v", err)
	}
}

// TestRegisterCommandFreshNameSucceeds guards against over-rejection: a plugin
// registering a genuinely new command name still succeeds.
func TestRegisterCommandFreshNameSucceeds(t *testing.T) {
	a := regTestApp(t)

	freshPlugin := func(app *App) error {
		return app.RegisterCommand(command.NewFuncCmd("phase1-fresh", "ok", func(ctx context.Context, args []string) (string, error) {
			return "", nil
		}))
	}
	if err := a.Use(freshPlugin); err != nil {
		t.Fatalf("fresh command registration must succeed, got %v", err)
	}
	if !a.IsRegisteredCommandInput("/phase1-fresh") {
		t.Fatalf("freshly registered command must be routable")
	}
}
