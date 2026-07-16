package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
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

func TestResolveCLILaunchReadsExplicitPersona(t *testing.T) {
	got, err := resolveCLILaunch([]string{"--persona", "coder"})
	if err != nil {
		t.Fatal(err)
	}
	if got.persona != "coder" {
		t.Fatalf("persona = %q, want coder", got.persona)
	}
}

func TestResolveCLILaunchDefaultsToNewChat(t *testing.T) {
	got, err := resolveCLILaunch(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.persona != "" || got.cont || got.resume != "" || got.pick {
		t.Fatalf("launch = %+v, want new default chat", got)
	}
}

func TestResolveCLILaunchRejectsContinueResumeConflict(t *testing.T) {
	if _, err := resolveCLILaunch([]string{"--continue", "--resume", "space"}); err == nil {
		t.Fatal("continue and resume conflict was accepted")
	}
}

func TestResolveCLILaunchResumeWithoutIDOpensPicker(t *testing.T) {
	got, err := resolveCLILaunch([]string{"--resume"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.pick || got.resume != "" {
		t.Fatalf("launch = %+v, want picker", got)
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
	got, err := resolveCLILaunch(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.persona != "" {
		t.Fatalf("default persona leaked into CLI launch: %+v", got)
	}
}

func TestResolveLaunchSpaceCreatesFreshChatAndContinueResumesIt(t *testing.T) {
	a := newPersonaTestApp(t)
	first, resumed, err := resolveLaunchSpace(a, cliLaunch{})
	if err != nil || resumed {
		t.Fatalf("first launch = %#v resumed=%v err=%v", first, resumed, err)
	}
	second, resumed, err := resolveLaunchSpace(a, cliLaunch{})
	if err != nil || resumed {
		t.Fatalf("second launch = %#v resumed=%v err=%v", second, resumed, err)
	}
	if first.ID == second.ID {
		t.Fatal("default launches reused the same Space")
	}
	if _, err := a.Spaces().AppendUserMessage(first.ID, "keep this", nil); err != nil {
		t.Fatal(err)
	}
	got, resumed, err := resolveLaunchSpace(a, cliLaunch{cont: true})
	if err != nil || !resumed || got.ID != first.ID {
		t.Fatalf("continue = %#v resumed=%v err=%v, want %s", got, resumed, err, first.ID)
	}
}

func TestPersonaLaunchScopesSessionToSpaceAndPersona(t *testing.T) {
	a := newPersonaTestApp(t, "coder")
	sp, resumed, err := resolveLaunchSpace(a, cliLaunch{persona: "coder"})
	if err != nil || resumed {
		t.Fatalf("launch = %#v resumed=%v err=%v", sp, resumed, err)
	}
	if sp.Kind != space.KindAgentDM || space.AgentParticipantID(sp) != "coder" {
		t.Fatalf("agent chat = %+v", sp)
	}
	source := cliSpaceSource(sp)
	if source != "cli:agent:"+sp.ID {
		t.Fatalf("source = %q", source)
	}
	if got := cliSessionSource(sp); got != source+":persona:coder" {
		t.Fatalf("session source = %q", got)
	}
}

func TestBareCLISourceSuppressesNoMentionHint(t *testing.T) {
	a := newPersonaTestApp(t, "tshoot")
	sp, _, err := resolveLaunchSpace(a, cliLaunch{})
	if err != nil {
		t.Fatal(err)
	}
	source := cliSpaceSource(sp)
	m := newShellModel(context.Background(), a, source)
	m.turnInput = "hello"

	before := len(m.items)
	m.addNoMentionHintIfNeeded(0)
	if len(m.items) != before {
		t.Fatalf("bare CLI should not emit no-mention hint, got %d items", len(m.items)-before)
	}
}
