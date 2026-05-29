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
	a := newPersonaTestApp(t, "default-bot", "coder")
	got := resolveCLISource(a, []string{"--persona", "coder"})
	if got != "cli:agent:coder" {
		t.Fatalf("source = %q, want cli:agent:coder", got)
	}
}

func TestResolveCLISourceFallsBackToDefaultPersona(t *testing.T) {
	a := newPersonaTestApp(t, "tshoot", "coder")
	got := resolveCLISource(a, nil)
	if got != "cli:agent:tshoot" && got != "cli:agent:coder" {
		t.Fatalf("source = %q, want cli:agent:<some persona>", got)
	}
}

func TestResolveCLISourceWithoutAnyPersonaKeepsLegacyCLI(t *testing.T) {
	a := newPersonaTestApp(t)
	got := resolveCLISource(a, nil)
	if got != "cli" {
		t.Fatalf("source = %q, want cli", got)
	}
}

func TestDefaultPersonaSourceSuppressesNoMentionHint(t *testing.T) {
	a := newPersonaTestApp(t, "tshoot")
	source := resolveCLISource(a, nil)
	m := newShellModel(context.Background(), a, source)
	m.turnInput = "hello"

	before := len(m.items)
	m.addNoMentionHintIfNeeded(0)
	if len(m.items) != before {
		t.Fatalf("default-persona DM should not emit hint, got %d items", len(m.items)-before)
	}
}
