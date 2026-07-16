package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	}, "@bob first", nil)
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
	}, "@bob second", nil)
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
	}, "@bob please implement this", nil)
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
	if _, err := a.interceptRoutedInput(context.Background(), "desktop:channel:"+ch.ID, "plain note", nil); err != nil {
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
	if _, err := a.interceptRoutedInput(ctx, "desktop:channel:"+ch.ID, "thread note", nil); err != nil {
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

func TestChannelWakeFailureDoesNotAppendSystemNotice(t *testing.T) {
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
	}, "@bob do it", nil)
	if res.err == nil || !strings.Contains(res.err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", res.err)
	}

	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Messages) != 1 {
		t.Fatalf("messages = %#v, want only original user message", sp.Messages)
	}
	if sp.Messages[0].ID != origin.ID {
		t.Fatalf("messages = %#v, want original user message only", sp.Messages)
	}
}

func TestInterceptRoutedInputWakeDoesNotCreateTaskBoardTask(t *testing.T) {
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
	done := make(chan struct{}, 1)
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "reply to " + turn.Input})
			select {
			case done <- struct{}{}:
			default:
			}
			return nil
		}), nil
	})

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	source := "desktop:channel:" + ch.ID
	if _, err := a.interceptRoutedInput(context.Background(), source, "@bob do it", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel wake")
	}

	tasks, err := a.Tasks().ListBySpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("channel wake created tasks = %#v, want none", tasks)
	}

	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawReply bool
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantAgent && m.AuthorID == "bob" && m.Content == "reply to @bob do it" {
			sawReply = true
			break
		}
	}
	if !sawReply {
		t.Fatalf("messages = %#v, want bob wake reply persisted", sp.Messages)
	}
}

func TestWakeContextProjectsCompleteSpaceHistory(t *testing.T) {
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
	a.syncWakeContext(s, "desktop:channel:"+ch.ID, ch.ID, "", "bob", current.ID)

	if strings.TrimSpace(s.Summary) != "" {
		t.Fatalf("space projection unexpectedly summarized history: %q", s.Summary)
	}
	if len(s.Messages) != 8 {
		t.Fatalf("messages = %d, want all 8 old messages: %+v", len(s.Messages), s.Messages)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Content, "current trigger") {
			t.Fatalf("current trigger leaked into wake context: %+v", m)
		}
	}
}

func TestContextViewFiltersNoisyRuntimeHistory(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
		Compact: config.CompactConfig{
			TriggerTokens: 10,
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
	base := time.Date(2026, 6, 15, 1, 2, 3, 0, time.UTC)
	add := func(m space.Message) {
		t.Helper()
		if m.AuthorID == "" {
			m.AuthorID = "user"
		}
		if m.AuthorKind == "" {
			m.AuthorKind = space.ParticipantUser
		}
		m.CreatedAt = base.Add(time.Duration(len(ch.Messages)) * time.Minute)
		written, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, m, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ch.Messages = append(ch.Messages, written)
	}
	add(space.Message{Content: "stable old context alpha beta gamma delta epsilon zeta eta theta"})
	add(space.Message{AuthorID: "system", AuthorKind: space.ParticipantSystem, Content: "Send failed: provider 400"})
	add(space.Message{AuthorID: "bob", AuthorKind: space.ParticipantAgent, Content: "NO_REPLY"})
	add(space.Message{AuthorID: "bob", AuthorKind: space.ParticipantAgent, Content: "Failed to deserialize the JSON body: unknown variant `image_url`, expected `text`"})
	add(space.Message{Content: "recent useful fact should stay"})
	current, err := a.Spaces().AppendUserMessage(ch.ID, "@bob current trigger should be excluded", []string{"bob"})
	if err != nil {
		t.Fatal(err)
	}

	view := a.BuildContextView(ContextViewInput{
		SpaceID:          ch.ID,
		Source:           "desktop:channel:" + ch.ID,
		AgentID:          "bob",
		ExcludeMessageID: current.ID,
	})
	var joined []string
	for _, m := range view.Messages {
		joined = append(joined, m.Content)
	}
	body := strings.Join(joined, "\n") + "\n" + view.Summary
	for _, bad := range []string{"Send failed", "NO_REPLY", "image_url", "current trigger"} {
		if strings.Contains(body, bad) {
			t.Fatalf("noisy context %q leaked into runtime evidence:\nmessages=%+v\nsummary=%s", bad, view.Messages, view.Summary)
		}
	}
	if !strings.Contains(strings.Join(joined, "\n"), "recent useful fact") {
		t.Fatalf("recent useful context missing: %+v", view.Messages)
	}
	if !strings.Contains(strings.Join(joined, "\n"), "stable old context") {
		t.Fatalf("old context was truncated: %+v", view.Messages)
	}
	if view.Summary != "" {
		t.Fatalf("space projection should not summarize history: %q", view.Summary)
	}
}
