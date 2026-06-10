package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/command"
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
		brief          string
		includeHistory bool
		disableResume  bool
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turns = append(turns, struct {
				source         string
				sessionID      string
				seen           []msg.Message
				brief          string
				includeHistory bool
				disableResume  bool
			}{
				source:         turn.Source,
				sessionID:      turn.Session.ID,
				seen:           append([]msg.Message(nil), turn.Session.Messages...),
				brief:          turn.CollaborationBrief,
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
	for _, want := range []string{
		"scope: channel",
		"trigger: explicit mention",
		"target agent: bob",
		"trigger message: user: @bob first",
		"chain budget remaining",
	} {
		if !strings.Contains(turns[0].brief, want) {
			t.Fatalf("first turn brief missing %q:\n%s", want, turns[0].brief)
		}
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
	updated, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := lastAgentMessageReason(updated, "bob"); got != "called by mention" {
		t.Fatalf("auto reply reason = %q, want called by mention", got)
	}
}

func TestChannelWakeCollaborationBriefIncludesAgentContext(t *testing.T) {
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

	for _, id := range []string{"bob", "iris"} {
		if _, err := a.Personas().Create(id, persona.Meta{Runtime: "stub"}, "# "+id); err != nil {
			t.Fatal(err)
		}
	}
	var brief string
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			brief = turn.CollaborationBrief
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(ch.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:        "iris",
		AuthorKind:      space.ParticipantAgent,
		Content:         "I think the UI state is unclear.",
		ParentMessageID: root.ID,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	trigger, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:        "iris",
		AuthorKind:      space.ParticipantAgent,
		Content:         "@bob please implement this",
		ParentMessageID: root.ID,
		Mentions:        []string{"bob"},
	}, []string{"bob"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := space.NewRoutingChain(root.ID, ch.ID, 2)
	chain.ParentMessageID = root.ID
	res := a.runChannelWake(context.Background(), "desktop:channel:"+ch.ID, ch.ID, space.RoutingTarget{
		AgentID:         "bob",
		OriginMessageID: trigger.ID,
		Chain:           chain,
	}, "@bob please implement this")
	if res.err != nil {
		t.Fatal(res.err)
	}
	for _, want := range []string{
		"scope: thread",
		"trigger: agent mention",
		"target agent: bob",
		"trigger message: iris: @bob please implement this",
		"chain budget remaining: 2",
		"recent agent conclusions:",
		"iris: I think the UI state is unclear.",
		"answer as part of this shared discussion",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
	updated, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := lastAgentMessageReason(updated, "bob"); got != "called by @iris" {
		t.Fatalf("auto reply reason = %q, want called by @iris", got)
	}
}

func lastAgentMessageReason(sp *space.Space, agentID string) string {
	for i := len(sp.Messages) - 1; i >= 0; i-- {
		m := sp.Messages[i]
		if m.AuthorKind == space.ParticipantAgent && m.AuthorID == agentID {
			return m.AutoReplyReason
		}
	}
	return ""
}

func TestRoutedNoTargetNoticePersistsToSpace(t *testing.T) {
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

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.interceptRoutedInput(context.Background(), "desktop:channel:"+ch.ID, "plain note"); err != nil {
		t.Fatal(err)
	}

	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + routing notice", sp.Messages)
	}
	if sp.Messages[0].AuthorKind != space.ParticipantUser || sp.Messages[0].Content != "plain note" {
		t.Fatalf("user message = %#v", sp.Messages[0])
	}
	if sp.Messages[1].AuthorKind != space.ParticipantSystem ||
		sp.Messages[1].Content != "No agent picked this up. Mention an agent or enable listening." {
		t.Fatalf("notice message = %#v", sp.Messages[1])
	}
}

func TestRoutedNoTargetThreadNoticePersistsAsReply(t *testing.T) {
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

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(ch.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := command.WithParentMessage(context.Background(), root.ID)
	if _, err := a.interceptRoutedInput(ctx, "desktop:channel:"+ch.ID, "thread note"); err != nil {
		t.Fatal(err)
	}

	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Messages) != 3 {
		t.Fatalf("messages = %#v, want root + user reply + routing notice reply", sp.Messages)
	}
	if sp.Messages[1].ParentMessageID != root.ID || sp.Messages[2].ParentMessageID != root.ID {
		t.Fatalf("thread messages = %#v, want notice under root", sp.Messages)
	}
	if sp.Messages[2].AuthorKind != space.ParticipantSystem {
		t.Fatalf("notice message = %#v", sp.Messages[2])
	}
}

func TestChannelWakeFailurePersistsToSpace(t *testing.T) {
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
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			return errors.New("boom")
		}), nil
	})

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	origin, err := a.Spaces().AppendUserMessage(ch.ID, "@bob do it", []string{"bob"})
	if err != nil {
		t.Fatal(err)
	}
	res := a.runChannelWake(context.Background(), "desktop:channel:"+ch.ID, ch.ID, space.RoutingTarget{
		AgentID:         "bob",
		OriginMessageID: origin.ID,
	}, "@bob do it")
	if res.err == nil || !strings.Contains(res.err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", res.err)
	}

	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + failure notice", sp.Messages)
	}
	if sp.Messages[1].AuthorKind != space.ParticipantSystem || sp.Messages[1].Content != "@bob failed: boom" {
		t.Fatalf("failure notice = %#v", sp.Messages[1])
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
