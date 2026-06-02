package telegram

import (
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

func newSpaceTestApp(t *testing.T) *app.App {
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
	return a
}

func TestFormatAgentMessageUsesDisplayPrefix(t *testing.T) {
	cases := []struct {
		name string
		line space.TranscriptLine
		want string
	}{
		{"with display", space.TranscriptLine{Display: "Coder", Content: "looking at it"}, "Coder: looking at it"},
		{"empty display falls back to body", space.TranscriptLine{Display: "", Content: "hi"}, "hi"},
		{"empty body returns empty", space.TranscriptLine{Display: "Coder", Content: " "}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAgentMessage(tc.line); got != tc.want {
				t.Fatalf("formatAgentMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewSpaceAgentRepliesSkipsPreExistingMessages(t *testing.T) {
	a := newSpaceTestApp(t)
	src := "tg:dm:42"
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().Resolve(src, space.PersonaInfo{ID: "tg:dm:42"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "old", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID, space.PersonaInfo{ID: "coder", Display: "Coder"}, "old reply", "", nil, ""); err != nil {
		t.Fatal(err)
	}

	before := spaceSnapshotIDs(a, src)

	if _, err := a.Spaces().AppendUserMessage(sp.ID, "new q", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID, space.PersonaInfo{ID: "coder", Display: "Coder"}, "fresh reply", "", nil, ""); err != nil {
		t.Fatal(err)
	}

	got := newSpaceAgentReplies(a, src, before)
	if len(got) != 1 {
		t.Fatalf("got %d replies, want 1: %#v", len(got), got)
	}
	if got[0] != "Coder: fresh reply" {
		t.Fatalf("reply = %q", got[0])
	}
}

func TestNoMentionHintForSourceMatchesRouterRules(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		input  string
		agents int
		want   bool
	}{
		{"tg dm no @ no agent", "tg:dm:42", "hi", 0, true},
		{"tg dm with @", "tg:dm:42", "@coder hi", 0, false},
		{"tg dm with agent", "tg:dm:42", "hi", 1, false},
		{"tg channel hint", "tg:channel:42", "hi", 0, true},
		{"agent dm no hint", "desktop:agent:tshoot", "hi", 0, false},
		{"unrouted no hint", "subtask:1", "hi", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := noMentionHintForSource(tc.src, tc.input, tc.agents) != ""
			if got != tc.want {
				t.Fatalf("hint fired = %v, want %v", got, tc.want)
			}
		})
	}
}
