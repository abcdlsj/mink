package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

func TestLooksSubstantive(t *testing.T) {
	cases := map[string]bool{
		"":                       false,
		"   ":                    false,
		"hi":                     false,
		"Hi":                     false,
		"在吗":                     false,
		"hey":                    false,
		"thanks":                 false,
		"hello":                  false,
		"please look at retry":   true,
		"retry policy review":    true,
		"重试策略需要审查":               true,
		"audit my code":          true,
		"帮我看看 retry":             true, // 9 chars, mixed lang, but specific
		"看一下":                    false,
		"看看":                     false,
	}
	for in, want := range cases {
		got := looksSubstantive(in)
		if got != want {
			t.Errorf("looksSubstantive(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDeriveAgentDMTitleTruncatesAndStripsTerminal(t *testing.T) {
	cases := map[string]string{
		"audit my retry policy":                           "audit my retry policy",
		"  hello world  ":                                 "hello world",
		"please review this code change.":                 "please review this code change",
		"重试策略需要审查；前后兼容":                                  "重试策略需要审查",
		"This is a very long first message that exceeds the title cap by miles for sure": "This is a very long first messag",
	}
	for in, want := range cases {
		got := deriveAgentDMTitle(in)
		if !strings.HasPrefix(got, want) {
			t.Errorf("deriveAgentDMTitle(%q) = %q, want prefix %q", in, got, want)
		}
		if utf8RuneCount(got) > MaxAgentDMTitleLen+1 {
			t.Errorf("deriveAgentDMTitle(%q) = %q, exceeds cap (%d runes, max %d)", in, got, utf8RuneCount(got), MaxAgentDMTitleLen+1)
		}
	}
}

func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func TestMaybeAutoTitleAgentDMSetsTitleAfterFirstSubstantiveTurn(t *testing.T) {
	a := newTitleTestApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "noted"})
			return nil
		}), nil
	})

	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-deadbeef", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "audit my retry logic", nil); err != nil {
		t.Fatal(err)
	}
	a.MaybeAutoTitleAgentDM(sp.ID)
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	if !strings.Contains(loaded.Title, "retry") && !strings.Contains(loaded.Title, "audit") {
		t.Fatalf("Title = %q, want a title derived from the first substantive user message", loaded.Title)
	}
}

func TestMaybeAutoTitleAgentDMSkipsLowInfoFirstMessage(t *testing.T) {
	a := newTitleTestApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-feedface", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "hi", nil); err != nil {
		t.Fatal(err)
	}
	a.MaybeAutoTitleAgentDM(sp.ID)
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	// Title should still be the machine seed (unchanged from EnsureSpace).
	// looksLikeAgentDMMachineSeed will treat that as "no real title".
	if !looksLikeAgentDMMachineSeed(loaded.Title, "coder") && loaded.Title != "" {
		t.Fatalf("Title = %q, want untouched (machine seed or empty) for low-info first turn", loaded.Title)
	}
}

func TestMaybeAutoTitleAgentDMLocksAfterFirstSuccess(t *testing.T) {
	a := newTitleTestApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-cafebabe", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "review the retry handler", nil); err != nil {
		t.Fatal(err)
	}
	a.MaybeAutoTitleAgentDM(sp.ID)
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	first := loaded.Title

	// Now a second user message arrives. The title MUST NOT change.
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "completely different topic now and very long", nil); err != nil {
		t.Fatal(err)
	}
	a.MaybeAutoTitleAgentDM(sp.ID)
	loaded2, _ := a.Spaces().LoadSpace(sp.ID)
	if loaded2.Title != first {
		t.Fatalf("Title flapped: first=%q, after second turn=%q", first, loaded2.Title)
	}
}

func TestMaybeAutoTitleAgentDMSkipsNonAgentDMSpace(t *testing.T) {
	a := newTitleTestApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "default", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "audit my code please", nil); err != nil {
		t.Fatal(err)
	}
	beforeTitle := sp.Title
	a.MaybeAutoTitleAgentDM(sp.ID)
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	if loaded.Title != beforeTitle {
		t.Fatalf("Channel title changed: %q -> %q (must not auto-title channels)", beforeTitle, loaded.Title)
	}
}

func TestMaybeAutoTitleAgentDMPublishesSpaceTitleChanged(t *testing.T) {
	a := newTitleTestApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-12345678", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "audit retry policy carefully", nil); err != nil {
		t.Fatal(err)
	}

	events, cancel := a.Bus().Subscribe(8)
	defer cancel()

	a.MaybeAutoTitleAgentDM(sp.ID)

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case ev := <-events:
			if ev.Type == bus.SpaceTitleChanged {
				if ev.SpaceID != sp.ID {
					t.Fatalf("SpaceID = %q, want %q", ev.SpaceID, sp.ID)
				}
				if strings.TrimSpace(ev.Text) == "" {
					t.Fatalf("Text empty: %+v", ev)
				}
				return
			}
		case <-deadline:
			t.Fatal("expected SpaceTitleChanged event not received within 500ms")
		}
	}
}

func newTitleTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{Runtime: "stub", DataDir: dir + "/data", Workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}
