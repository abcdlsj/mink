package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
)

func TestFlagPersonaParsesEveryShape(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"none", nil, ""},
		{"long flag space", []string{"--persona", "coder"}, "coder"},
		{"long flag eq", []string{"--persona=coder"}, "coder"},
		{"short -p space", []string{"-p", "coder"}, "coder"},
		{"short -p eq", []string{"-p=coder"}, "coder"},
		{"single-dash long", []string{"-persona", "coder"}, "coder"},
		{"trailing flag no value", []string{"--persona"}, ""},
		{"flag among others", []string{"--debug", "--persona", "tshoot", "--quiet"}, "tshoot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := flagPersona(tc.args); got != tc.want {
				t.Fatalf("flagPersona(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func newPersonaTestApp(t *testing.T, ids ...string) *app.App {
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
	for _, id := range ids {
		if _, err := a.Personas().Create(id, persona.Meta{Display: id, Runtime: "stub"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func TestResolveCLISourcePrefersExplicitPersonaFlag(t *testing.T) {
	got := resolveCLISource([]string{"--persona", "coder"})
	if got != "cli:agent:coder" {
		t.Fatalf("source = %q, want cli:agent:coder", got)
	}
}

func TestResolveCLISourceWithoutFlagUsesDefaultCLI(t *testing.T) {
	got := resolveCLISource(nil)
	if got != "cli" {
		t.Fatalf("source = %q, want cli", got)
	}
}

func TestResolveCLISourceAlwaysReturnsNonEmptyLaunchSource(t *testing.T) {
	cases := [][]string{
		nil,
		{"--persona", "coder"},
	}
	for _, args := range cases {
		if got := resolveCLISource(args); got == "" {
			t.Fatalf("resolveCLISource(%v) returned empty source", args)
		}
	}
}

func TestResolveCLISourceWithoutFlagIgnoresConfigDefaultPersona(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:        "stub",
		DataDir:        filepath.Join(dir, "sumi-data"),
		Workspace:      dir,
		DefaultPersona: "tshoot",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	for _, id := range []string{"tshoot", "coder"} {
		if _, err := a.Personas().Create(id, persona.Meta{Display: id, Runtime: "stub"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	got := resolveCLISource(nil)
	if got != "cli" {
		t.Fatalf("source = %q, want cli", got)
	}
}

func TestResolveCLISourceWithoutAnyPersonaKeepsLegacyCLI(t *testing.T) {
	got := resolveCLISource(nil)
	if got != "cli" {
		t.Fatalf("source = %q, want cli", got)
	}
}

func TestBareCLISourceSuppressesNoMentionHint(t *testing.T) {
	a := newPersonaTestApp(t, "tshoot")
	source := resolveCLISource(nil)
	m := newShellModel(context.Background(), a, source)
	m.turnInput = "hello"

	before := len(m.items)
	m.addNoMentionHintIfNeeded(0)
	if len(m.items) != before {
		t.Fatalf("bare CLI should not emit no-mention hint, got %d items", len(m.items)-before)
	}
}
