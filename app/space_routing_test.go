package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

func TestChannelWakeUsesStablePersonaSessionWithSpaceContext(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("bob", persona.Meta{Runtime: "stub"}, "# Bob"); err != nil {
		t.Fatal(err)
	}

	var turns []struct {
		source         string
		sessionID      string
		seen           []msg.Message
		includeHistory bool
		disableResume  bool
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turns = append(turns, struct {
				source         string
				sessionID      string
				seen           []msg.Message
				includeHistory bool
				disableResume  bool
			}{
				source:         turn.Source,
				sessionID:      turn.Session.ID,
				seen:           append([]msg.Message(nil), turn.Session.Messages...),
				includeHistory: turn.IncludeHistory,
				disableResume:  turn.DisableExternalResume,
			})
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "reply to " + turn.Input})
			return nil
		}), nil
	})

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	source := "desktop:channel:" + ch.ID

	first, err := a.Spaces().AppendUserMessage(ch.ID, "@bob first", []string{"bob"})
	if err != nil {
		t.Fatal(err)
	}
	res1 := a.runChannelWake(context.Background(), source, ch.ID, space.RoutingTarget{
		AgentID:         "bob",
		OriginMessageID: first.ID,
	}, "@bob first")
	if res1.err != nil {
		t.Fatal(res1.err)
	}

	second, err := a.Spaces().AppendUserMessage(ch.ID, "@bob second", []string{"bob"})
	if err != nil {
		t.Fatal(err)
	}
	res2 := a.runChannelWake(context.Background(), source, ch.ID, space.RoutingTarget{
		AgentID:         "bob",
		OriginMessageID: second.ID,
	}, "@bob second")
	if res2.err != nil {
		t.Fatal(res2.err)
	}

	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(turns))
	}
	wantSource := source + ":persona:bob"
	if turns[0].source != wantSource || turns[1].source != wantSource {
		t.Fatalf("sources = %q / %q, want %q", turns[0].source, turns[1].source, wantSource)
	}
	if turns[0].sessionID == "" || turns[1].sessionID != turns[0].sessionID {
		t.Fatalf("session ids = %q / %q, want stable", turns[0].sessionID, turns[1].sessionID)
	}
	if !turns[0].includeHistory || !turns[1].includeHistory {
		t.Fatalf("IncludeHistory flags = %v / %v, want true", turns[0].includeHistory, turns[1].includeHistory)
	}
	if !turns[0].disableResume || !turns[1].disableResume {
		t.Fatalf("DisableExternalResume flags = %v / %v, want true", turns[0].disableResume, turns[1].disableResume)
	}
	if len(turns[0].seen) != 0 {
		t.Fatalf("first turn context = %d, want 0", len(turns[0].seen))
	}
	if len(turns[1].seen) < 2 {
		t.Fatalf("second turn context too small: %+v", turns[1].seen)
	}
	if turns[1].seen[0].Role != "user" || turns[1].seen[0].Content != "[user] @bob first" {
		t.Fatalf("first context message = %+v", turns[1].seen[0])
	}
	if turns[1].seen[1].Role != "assistant" || turns[1].seen[1].Content != "reply to @bob first" {
		t.Fatalf("second context message = %+v", turns[1].seen[1])
	}
}

func TestWakeContextUsesTokenBudgetAndSummary(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
		Compact: config.CompactConfig{
			TriggerTokens: 20,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := a.Spaces().AppendUserMessage(ch.ID, "old detail abcdefghijklmnop "+string(rune('a'+i)), nil); err != nil {
			t.Fatal(err)
		}
	}
	current, err := a.Spaces().AppendUserMessage(ch.ID, "@bob current trigger should be excluded", []string{"bob"})
	if err != nil {
		t.Fatal(err)
	}

	s := session.New("desktop:channel:" + ch.ID + ":persona:bob")
	a.syncWakeContext(s, ch.ID, "", "bob", current.ID)

	if strings.TrimSpace(s.Summary) == "" {
		t.Fatal("expected compact summary for dropped old context")
	}
	if got := estimateMessages(s.Messages); got > 20 {
		t.Fatalf("context tokens = %d, want <= 20; messages=%+v", got, s.Messages)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Content, "current trigger") {
			t.Fatalf("current trigger leaked into wake context: %+v", m)
		}
	}
	if len(s.Messages) >= 8 {
		t.Fatalf("expected token budget to drop old messages, kept %d", len(s.Messages))
	}
}
