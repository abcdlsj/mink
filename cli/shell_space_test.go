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

func seedAgentMessage(t *testing.T, a *app.App, sp *space.Space, agentID, display, content string) {
	t.Helper()
	if _, err := a.Personas().Create(agentID, persona.Meta{Display: display, Runtime: "stub"}, ""); err != nil {
		t.Fatalf("create persona: %v", err)
	}
	info := space.PersonaInfo{ID: agentID, Display: display}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "ping", nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID, info, content, "", nil, ""); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

func TestLoadSpaceTranscriptRendersAuthorPrefix(t *testing.T) {
	a := newSpaceTestApp(t)
	sp, err := a.Spaces().EnsureForSource("cli", space.PersonaInfo{ID: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	seedAgentMessage(t, a, sp, "coder", "Coder", "looking")

	m := newShellModel(context.Background(), a, "cli")
	m.loadSpaceTranscript()

	if len(m.items) != 2 {
		t.Fatalf("items = %d, want 2: %#v", len(m.items), m.items)
	}
	if m.items[0].Kind != itemUser {
		t.Fatalf("first kind = %d, want user", m.items[0].Kind)
	}
	if m.items[1].Kind != itemAssistant {
		t.Fatalf("second kind = %d, want assistant", m.items[1].Kind)
	}
	if m.items[1].Author != "Coder" {
		t.Fatalf("assistant author = %q, want Coder", m.items[1].Author)
	}
}

func TestRenderAssistantItemPrefixesAuthor(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 80
	item := &chatItem{
		Kind:   itemAssistant,
		Author: "Coder",
		Segments: []chatSegment{
			{Kind: segText, Text: "looking at it"},
		},
	}

	out := stripANSI(joinLines(m.renderItem(item, 0)))
	if !contains(out, "Coder") {
		t.Fatalf("render output missing author label:\n%s", out)
	}
	if !contains(out, "looking at it") {
		t.Fatalf("render output missing body:\n%s", out)
	}
}

func TestNoMentionHintFiresOnlyForRoutedSourcesWithoutAt(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		input    string
		agents   int
		wantHint bool
	}{
		{"cli no @ no agent", "cli", "hello", 0, true},
		{"cli with @ no agent", "cli", "@coder hi", 0, false},
		{"cli with agent reply", "cli", "hello", 1, false},
		{"agent dm no @ no agent", "cli:agent:tshoot", "hello", 0, false},
		{"tg dm no @ no agent", "tg:dm:42", "hello", 0, true},
		{"tg channel @", "tg:channel:42", "@coder", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newShellModel(context.Background(), nil, tc.source)
			m.turnInput = tc.input
			before := len(m.items)
			m.addNoMentionHintIfNeeded(tc.agents)
			fired := len(m.items) > before
			if fired != tc.wantHint {
				t.Fatalf("hint fired = %v, want %v", fired, tc.wantHint)
			}
		})
	}
}

func TestPersonaDMRendersAuthorPrefixFromAgentSpace(t *testing.T) {
	a := newSpaceTestApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "tshoot", space.PersonaInfo{ID: "tshoot", Display: "Tshoot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Personas().Create("tshoot", persona.Meta{Display: "Tshoot", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "hi", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID, space.PersonaInfo{ID: "tshoot", Display: "Tshoot"}, "checking", "", nil, ""); err != nil {
		t.Fatal(err)
	}

	m := newShellModel(context.Background(), a, "cli:agent:tshoot")
	m.loadSpaceTranscript()
	if len(m.items) != 2 {
		t.Fatalf("items = %d, want 2", len(m.items))
	}
	if m.items[1].Author != "Tshoot" {
		t.Fatalf("agent author = %q, want Tshoot", m.items[1].Author)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

func stripANSI(s string) string {
	var out []rune
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
