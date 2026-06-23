package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func TestSplitModel(t *testing.T) {
	cases := []struct {
		in       string
		provider string
		model    string
	}{
		{"anthropic / claude-sonnet-4", "anthropic", "claude-sonnet-4"},
		{"openai / gpt-4.1-mini", "openai", "gpt-4.1-mini"},
		{"(unconfigured)", "(unconfigured)", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		p, m := splitModel(c.in)
		if p != c.provider || m != c.model {
			t.Errorf("splitModel(%q) = %q,%q want %q,%q", c.in, p, m, c.provider, c.model)
		}
	}
}

func TestFallback(t *testing.T) {
	if got := fallback("", "default"); got != "default" {
		t.Errorf("fallback empty: %q", got)
	}
	if got := fallback("  ", "default"); got != "default" {
		t.Errorf("fallback whitespace: %q", got)
	}
	if got := fallback("real", "default"); got != "real" {
		t.Errorf("fallback non-empty: %q", got)
	}
}

func TestListToolsReturnsRegistryTools(t *testing.T) {
	b, _ := newBackendWithApp(t)
	tools := b.ListTools()
	if len(tools) == 0 {
		t.Fatal("tools should expose registry tools")
	}
	var gotBash bool
	for _, tool := range tools {
		if tool.Name == "bash" && tool.Enabled {
			gotBash = true
		}
	}
	if !gotBash {
		t.Fatalf("tools = %#v, want enabled bash tool", tools)
	}
}

func TestIsThreadIDDistinguishesChannel(t *testing.T) {
	if isThreadID(defaultChannelID) {
		t.Error("default channel id must not look like a thread id")
	}
	if !isThreadID("20260528-desktop-abcdef12") {
		t.Error("session-shaped id should be a thread id")
	}
}

func TestBaseMessageViewCarriesRuntimeMeta(t *testing.T) {
	sp := space.New(space.KindAgentDM, "coder", []space.Participant{
		{ID: "coder", Kind: space.ParticipantAgent, Display: "Coder"},
	})
	msg := space.Message{
		ID:          "m1",
		AuthorID:    "coder",
		AuthorKind:  space.ParticipantAgent,
		Content:     "ok",
		RuntimeMeta: map[string]string{"runtime": "claude", "cli_version": "claude 2.0"},
	}

	view := baseMessageView(sp, msg, nil)

	if view.RuntimeMeta["runtime"] != "claude" || view.RuntimeMeta["cli_version"] != "claude 2.0" {
		t.Fatalf("runtime meta = %#v", view.RuntimeMeta)
	}
}

func TestToBusEventMapsCollab(t *testing.T) {
	cases := []struct {
		name string
		in   bus.Event
		want string
	}{
		{"queued", bus.Event{Type: bus.DelegateQueued, TaskID: "t1", Text: "audit retry"}, "agent.delegate.started"},
		{"started", bus.Event{Type: bus.DelegateStarted, TaskID: "t1"}, "agent.delegate.progress"},
		{"finished", bus.Event{Type: bus.DelegateFinished, TaskID: "t1", Output: "done"}, "agent.delegate.finished"},
		{"failed", bus.Event{Type: bus.DelegateFailed, TaskID: "t1", Err: "oops"}, "agent.delegate.failed"},
		{"mention call", bus.Event{Type: bus.ToolCallStarted, Tool: "mention", Input: `{"target":"coder"}`}, "agent.mention"},
		{"mention reply", bus.Event{Type: bus.ToolCallFinished, Tool: "mention", Output: "ok", Input: `{"target":"coder"}`}, "agent.mention.reply"},
		{"plain tool", bus.Event{Type: bus.ToolCallStarted, Tool: "shell"}, bus.ToolCallStarted},
	}
	for _, c := range cases {
		got := toBusEvent(c.in).Type
		if got != c.want {
			t.Errorf("%s: toBusEvent type = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMentionTargetReadsArgs(t *testing.T) {
	got := mentionTarget(bus.Event{Tool: "mention", Input: `{"target":"reviewer"}`})
	if got != "reviewer" {
		t.Errorf("mentionTarget = %q, want reviewer", got)
	}
	got = mentionTarget(bus.Event{Tool: "mention", Input: `{"agent":"coder"}`})
	if got != "coder" {
		t.Errorf("mentionTarget agent fallback = %q", got)
	}
	got = mentionTarget(bus.Event{Tool: "mention", Input: ""})
	if got != "mention" {
		t.Errorf("mentionTarget empty fallback = %q", got)
	}
}

func TestAgentDefaultDMAndNamedChatsAreListedSeparately(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	if direct := b.ListDirectChats(); len(direct) != 1 || direct[0].Kind != "direct_chat" || direct[0].Title != "Sumi" {
		t.Fatalf("initial direct chats should only contain Sumi: %#v", direct)
	}

	defaultDetail := b.GetAgentDM("coder")
	if defaultDetail.Item.ID == "" {
		t.Fatal("default agent dm was not created")
	}

	direct := b.ListDirectChats()
	if len(direct) != 2 {
		t.Fatalf("direct chats = %d, want Sumi + default agent dm: %#v", len(direct), direct)
	}
	var gotAgentDM bool
	for _, item := range direct {
		if item.Kind == "agent_dm" && item.PersonaID == "coder" && item.Title == "@Coder" {
			gotAgentDM = true
		}
	}
	if !gotAgentDM {
		t.Fatalf("default agent dm missing from direct chats: %#v", direct)
	}
	if got := b.ListAgentDMs(); len(got) != 0 {
		t.Fatalf("default dm leaked into agent chats: %#v", got)
	}

	named, err := b.CreateAgentDM("coder", "UI overhaul")
	if err != nil {
		t.Fatal(err)
	}
	if named.Title != "UI overhaul" {
		t.Fatalf("named title = %q", named.Title)
	}
	chats := b.ListAgentDMs()
	if len(chats) != 1 || chats[0].ID != named.ID {
		t.Fatalf("agent chats = %#v, want named chat %s", chats, named.ID)
	}
}

func TestDefaultAgentDMSendUsesExistingSpaceID(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(_ context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok: " + turn.Input})
			return nil
		}), nil
	})

	defaultDetail := b.GetAgentDM("coder")
	if defaultDetail.Item.ID == "" || defaultDetail.Item.PersonaID != "coder" {
		t.Fatalf("default detail = %#v, want coder agent dm", defaultDetail.Item)
	}
	if _, err := b.SendMessage(SendRequest{
		SessionID: defaultDetail.Item.ID,
		PersonaID: defaultDetail.Item.PersonaID,
		Input:     "hello",
	}); err != nil {
		t.Fatal(err)
	}

	detail := b.GetAgentDM(defaultDetail.Item.ID)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + assistant in existing default dm", detail.Messages)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Content != "hello" {
		t.Fatalf("user message = %#v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "agent" || detail.Messages[1].AuthorID != "coder" || detail.Messages[1].Content != "ok: hello" {
		t.Fatalf("assistant message = %#v", detail.Messages[1])
	}

	spaces, err := a.Spaces().ListSpaces()
	if err != nil {
		t.Fatal(err)
	}
	var agentDMs int
	for _, sp := range spaces {
		if sp.Kind == space.KindAgentDM {
			agentDMs++
		}
	}
	if agentDMs != 1 {
		t.Fatalf("agent dm spaces = %d, want one existing default dm", agentDMs)
	}
}

func TestNamedAgentChatSendUsesExistingSpaceID(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(_ context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "named ok: " + turn.Input})
			return nil
		}), nil
	})

	named, err := b.CreateAgentDM("coder", "UI overhaul")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendMessage(SendRequest{
		SessionID: named.ID,
		PersonaID: named.PersonaID,
		Input:     "hello named",
	}); err != nil {
		t.Fatal(err)
	}

	detail := b.GetAgentDM(named.ID)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + assistant in existing named chat", detail.Messages)
	}
	if detail.Messages[1].Role != "agent" || detail.Messages[1].AuthorID != "coder" || detail.Messages[1].Content != "named ok: hello named" {
		t.Fatalf("assistant message = %#v", detail.Messages[1])
	}
	if chats := b.ListAgentDMs(); len(chats) != 1 || chats[0].ID != named.ID {
		t.Fatalf("agent chats = %#v, want only named chat %s", chats, named.ID)
	}
}

func TestDirectChatsIncludeDefaultSumiConversation(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}

	direct := b.ListDirectChats()
	if len(direct) != 1 {
		t.Fatalf("direct chats = %d, want default Sumi only: %#v", len(direct), direct)
	}
	if direct[0].Kind != "direct_chat" || direct[0].Title != "Sumi" || direct[0].PersonaID != "" {
		t.Fatalf("default direct item = %#v", direct[0])
	}

	if _, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindDirectChat, "Sumi"); err != nil {
		t.Fatalf("default Sumi space missing: %v", err)
	}
	if sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindAgentDM, "coder"); err != nil || sp != nil {
		t.Fatalf("default Sumi listing should not create agent dm, got space=%#v err=%v", sp, err)
	}
}

func TestDefaultSumiDirectSendUsesExistingSpaceID(t *testing.T) {
	b, a := newBackendWithApp(t)
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(_ context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "sumi ok: " + turn.Input})
			return nil
		}), nil
	})

	direct := b.ListDirectChats()
	if len(direct) != 1 {
		t.Fatalf("direct chats = %#v, want default Sumi only", direct)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: direct[0].ID, Input: "hello sumi"}); err != nil {
		t.Fatal(err)
	}

	detail := b.GetDirectChat(direct[0].ID)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + assistant in existing Sumi direct", detail.Messages)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Content != "hello sumi" {
		t.Fatalf("user message = %#v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "agent" || detail.Messages[1].AuthorID != "assistant" || detail.Messages[1].Content != "sumi ok: hello sumi" {
		t.Fatalf("assistant message = %#v", detail.Messages[1])
	}
	if direct = b.ListDirectChats(); len(direct) != 1 || direct[0].ID != detail.Item.ID {
		t.Fatalf("direct chats = %#v, want existing Sumi direct only", direct)
	}
}

func TestDefaultSumiDirectTreatsMentionsAsText(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("bob", persona.Meta{Display: "Bob", Runtime: "bob-rt"}, "bob"); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(_ context.Context, turn *agent.Turn) error {
			gotInput = turn.Input
			turn.Session.Add(msg.Message{Role: "assistant", Content: "sumi ok"})
			return nil
		}), nil
	})
	a.RegisterRuntime("bob-rt", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("default Sumi direct mention should not route to Bob")
		return nil, nil
	})

	direct := b.ListDirectChats()
	if len(direct) != 1 {
		t.Fatalf("direct chats = %#v, want default Sumi only", direct)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: direct[0].ID, PersonaID: "bob", Input: "@bob hello"}); err != nil {
		t.Fatal(err)
	}
	if gotInput != "@bob hello" {
		t.Fatalf("input = %q, want @bob hello", gotInput)
	}
}

func TestDefaultSumiSendFailurePersistsNotice(t *testing.T) {
	b, a := newBackendWithApp(t)
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(context.Context, *agent.Turn) error {
			return errors.New("boom")
		}), nil
	})

	direct := b.ListDirectChats()
	if len(direct) != 1 {
		t.Fatalf("direct chats = %#v, want default Sumi", direct)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: direct[0].ID, Input: "hello"}); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}

	detail := b.GetDirectChat(direct[0].ID)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + persisted failure notice", detail.Messages)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Content != "hello" {
		t.Fatalf("user message = %#v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "system" || detail.Messages[1].Content != "Send failed: boom" {
		t.Fatalf("failure notice = %#v", detail.Messages[1])
	}
	if direct = b.ListDirectChats(); len(direct) != 1 || len(direct[0].Agents) != 0 {
		t.Fatalf("default Sumi direct agents = %#v, want none", direct)
	}
}

func TestDefaultSumiDirectIgnoresPersonaID(t *testing.T) {
	b, a := newBackendWithApp(t)
	var gotInput string
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(_ context.Context, turn *agent.Turn) error {
			gotInput = turn.Input
			turn.Session.Add(msg.Message{Role: "assistant", Content: "sumi ok"})
			return nil
		}), nil
	})

	direct := b.ListDirectChats()
	if len(direct) != 1 {
		t.Fatalf("direct chats = %#v, want default Sumi", direct)
	}
	if _, err := b.SendMessage(SendRequest{SessionID: direct[0].ID, PersonaID: "assistant", Input: "still there?"}); err != nil {
		t.Fatal(err)
	}
	if gotInput != "still there?" {
		t.Fatalf("input = %q, want still there?", gotInput)
	}

	detail := b.GetDirectChat(direct[0].ID)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + assistant", detail.Messages)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Content != "still there?" {
		t.Fatalf("user message = %#v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "agent" || detail.Messages[1].AuthorID != "assistant" || detail.Messages[1].Content != "sumi ok" {
		t.Fatalf("assistant message = %#v", detail.Messages[1])
	}
}

func TestNewDirectChatWithAgentAddsVisibleParticipant(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub", Description: "writes code"}, ""); err != nil {
		t.Fatal(err)
	}

	detail, err := b.NewDirectChat("Pairing", "coder")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Item.ID == "" || detail.Item.Title != "Pairing" || detail.Item.TitleFixed {
		t.Fatalf("detail item = %#v", detail.Item)
	}

	var gotDirect *DirectChatItem
	for _, item := range b.ListDirectChats() {
		if item.ID == detail.Item.ID {
			got := item
			gotDirect = &got
			break
		}
	}
	if gotDirect == nil {
		t.Fatalf("new direct chat missing from list")
	}
	if len(gotDirect.Agents) != 1 || gotDirect.Agents[0] != "coder" {
		t.Fatalf("direct agents = %#v, want coder", gotDirect.Agents)
	}

	participants := b.GetParticipants(detail.Item.ID, "")
	if len(participants.Agents) != 1 || participants.Agents[0].ID != "coder" || participants.Agents[0].Display != "Coder" {
		t.Fatalf("participants = %#v, want visible coder", participants.Agents)
	}
}

func TestNewDirectChatRejectsUnknownAgent(t *testing.T) {
	b, _ := newBackendWithApp(t)
	if _, err := b.NewDirectChat("Pairing", "missing"); err == nil {
		t.Fatal("expected unknown agent error")
	}
}

func TestListDirectChatsKeepsExplicitEmptyChats(t *testing.T) {
	b, _ := newBackendWithApp(t)
	first, err := b.NewDirectChat("First empty", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.NewDirectChat("Second empty", "")
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, item := range b.ListDirectChats() {
		seen[item.ID] = true
	}
	if !seen[first.Item.ID] || !seen[second.Item.ID] {
		t.Fatalf("explicit empty direct chats missing from list: seen=%#v first=%s second=%s", seen, first.Item.ID, second.Item.ID)
	}
}

func TestDeleteAgentChatRemovesSpaceSessionsAndTasks(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	named, err := b.CreateAgentDM("coder", "cleanup target")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := a.CurrentSession("desktop:agent:" + named.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess.Add(msg.Message{Role: "user", Content: "remember me"})
	if err := a.SaveSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          named.ID,
		TriggerMessageID: "msg-1",
		InitiatorID:      "user",
		WorkerID:         "coder",
		Title:            "delete me",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := b.DeleteConversation(DeleteConversationRequest{Kind: "agent_dm", ID: named.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !res.DeletedSpace || res.DeletedSessions != 1 || res.DeletedTasks != 1 {
		t.Fatalf("delete result = %+v", res)
	}
	if _, err := a.Spaces().LoadSpace(named.ID); err == nil {
		t.Fatalf("agent chat space still loads after delete")
	}
	if got := a.Personas().Get("coder"); got == nil {
		t.Fatalf("persona should be preserved")
	}
	if chats := b.ListAgentDMs(); len(chats) != 0 {
		t.Fatalf("agent chats after delete = %#v, want empty", chats)
	}
	if sessions, err := a.ListSessions(); err != nil {
		t.Fatal(err)
	} else if len(sessions) != 0 {
		t.Fatalf("sessions after delete = %#v, want empty", sessions)
	}
	if tasks, err := a.Tasks().ListBySpace(named.ID); err != nil {
		t.Fatal(err)
	} else if len(tasks) != 0 {
		t.Fatalf("tasks after delete = %#v, want empty", tasks)
	}
}

func TestGetAgentDMStaleSpaceIDDoesNotCreateBogusDM(t *testing.T) {
	b, a := newBackendWithApp(t)
	staleID := "20260612-agent-10ff4668"

	detail := b.GetAgentDM(staleID)
	if detail.Item.ID != "" {
		t.Fatalf("detail = %#v, want empty for stale space id", detail.Item)
	}
	spaces, err := a.Spaces().ListSpaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range spaces {
		if sp.Kind == space.KindAgentDM {
			t.Fatalf("stale space id created bogus agent dm: %#v", sp)
		}
	}
}

func TestGetAgentDMSourceLikeIDDoesNotCreateBogusDM(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	detail := b.GetAgentDM("coder")
	if detail.Item.ID == "" {
		t.Fatal("agent dm should be created for registered persona")
	}
	bogus := b.GetAgentDM("desktop:agent:" + detail.Item.ID)
	if bogus.Item.ID != "" {
		t.Fatalf("bogus detail = %#v, want empty", bogus.Item)
	}
	spaces, err := a.Spaces().ListSpaces()
	if err != nil {
		t.Fatal(err)
	}
	var agentDMs int
	for _, sp := range spaces {
		if sp.Kind == space.KindAgentDM {
			agentDMs++
		}
	}
	if agentDMs != 1 {
		t.Fatalf("agent dm spaces = %d, want only original", agentDMs)
	}
}

func TestRetryFailedAgentMessageReusesUserMessage(t *testing.T) {
	b, a := newBackendWithApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(_ context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "retry ok: " + turn.Input})
			return nil
		}), nil
	})
	detail := b.GetAgentDM("coder")
	user, err := a.Spaces().AppendUserMessage(detail.Item.ID, "retry this", nil)
	if err != nil {
		t.Fatal(err)
	}
	failed, _, err := a.Spaces().AppendMessageWithRouting(detail.Item.ID, space.Message{
		AuthorID:   "coder",
		AuthorKind: space.ParticipantAgent,
		Status:     "failed",
		Error:      "interrupted",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.RetryMessage(RetryMessageRequest{SpaceID: detail.Item.ID, MessageID: failed.ID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	got := b.GetAgentDM(detail.Item.ID)
	var users, failedMessages int
	var assistantOK bool
	for _, m := range got.Messages {
		if m.Role == "user" {
			users++
			if m.ID != user.ID {
				t.Fatalf("unexpected user message = %#v", m)
			}
		}
		if m.Status == "failed" || m.Status == "pending" {
			failedMessages++
		}
		if m.Role == "agent" && m.Content == "retry ok: retry this" {
			assistantOK = true
		}
	}
	if users != 1 {
		t.Fatalf("user message count = %d, want 1; messages=%#v", users, got.Messages)
	}
	if failedMessages != 0 {
		t.Fatalf("failed/pending messages = %d, want none; messages=%#v", failedMessages, got.Messages)
	}
	if !assistantOK {
		t.Fatalf("retried assistant output missing: %#v", got.Messages)
	}
}

func TestRetryFailedChannelAgentMessagePersistsReply(t *testing.T) {
	b, a := newBackendWithApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(*agent.RuntimeEnv) (agent.Runtime, error) {
		return desktopRuntimeFunc(func(_ context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "channel retry ok: " + turn.Input})
			return nil
		}), nil
	})
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.Spaces().AppendUserMessage(sp.ID, "retry channel", nil)
	if err != nil {
		t.Fatal(err)
	}
	failed, _, err := a.Spaces().AppendMessageWithRouting(sp.ID, space.Message{
		AuthorID:   "coder",
		AuthorKind: space.ParticipantAgent,
		Status:     "failed",
		Error:      "interrupted",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := b.RetryMessage(RetryMessageRequest{SpaceID: sp.ID, MessageID: failed.ID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)

	got := b.GetChannel(sp.ID)
	var users, failedMessages int
	var assistantOK bool
	for _, m := range got.Messages {
		if m.Role == "user" {
			users++
			if m.ID != user.ID {
				t.Fatalf("unexpected user message = %#v", m)
			}
		}
		if m.Status == "failed" || m.Status == "pending" {
			failedMessages++
		}
		if m.Role == "agent" && m.AuthorID == "coder" && m.Content == "channel retry ok: retry channel" {
			assistantOK = true
		}
	}
	if users != 1 {
		t.Fatalf("user message count = %d, want 1; messages=%#v", users, got.Messages)
	}
	if failedMessages != 0 {
		t.Fatalf("failed/pending messages = %d, want none; messages=%#v", failedMessages, got.Messages)
	}
	if !assistantOK {
		t.Fatalf("retried channel assistant output missing: %#v", got.Messages)
	}
}

func TestGetAgentDMDropsSupersededFailedRetryPlaceholder(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	detail := b.GetAgentDM("coder")
	if _, err := a.Spaces().AppendUserMessage(detail.Item.ID, "explain next steps", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(detail.Item.ID, space.Message{
		AuthorID:   "coder",
		AuthorKind: space.ParticipantAgent,
		Status:     "failed",
		Error:      "stale retry placeholder",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(detail.Item.ID, space.Message{
		AuthorID:   "coder",
		AuthorKind: space.ParticipantAgent,
		Content:    "real successful answer",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	got := b.GetAgentDM(detail.Item.ID)
	var failedMessages, successfulMessages int
	for _, m := range got.Messages {
		if m.Status == "failed" || m.Status == "pending" {
			failedMessages++
		}
		if m.Role == "agent" && m.Content == "real successful answer" {
			successfulMessages++
		}
	}
	if failedMessages != 0 {
		t.Fatalf("failed/pending messages = %d, want stale retry placeholder dropped; messages=%#v", failedMessages, got.Messages)
	}
	if successfulMessages != 1 {
		t.Fatalf("successful messages = %d, want 1; messages=%#v", successfulMessages, got.Messages)
	}
}

func TestGetAgentDMKeepsRecentCrossProcessPendingMessage(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	detail := b.GetAgentDM("coder")
	if _, err := a.Spaces().AppendUserMessage(detail.Item.ID, "long running request", nil); err != nil {
		t.Fatal(err)
	}
	pending, _, err := a.Spaces().AppendMessageWithRouting(detail.Item.ID, space.Message{
		AuthorID:    "coder",
		AuthorKind:  space.ParticipantAgent,
		Content:     "working",
		Status:      "pending",
		RuntimeMeta: map[string]string{pendingMetaStreamID: "stream-other-process"},
		CreatedAt:   time.Now().Add(-pendingRecoveryGrace / 2),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := b.GetAgentDM(detail.Item.ID)
	var found bool
	for _, m := range got.Messages {
		if m.ID != pending.ID {
			continue
		}
		found = true
		if m.Status != "pending" || m.Error != "" {
			t.Fatalf("recent cross-process pending = %#v, want pending without error", m)
		}
	}
	if !found {
		t.Fatalf("pending message %s missing: %#v", pending.ID, got.Messages)
	}
}

func TestGetAgentDMFailsExpiredPendingMessage(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	detail := b.GetAgentDM("coder")
	if _, err := a.Spaces().AppendUserMessage(detail.Item.ID, "abandoned request", nil); err != nil {
		t.Fatal(err)
	}
	pending, _, err := a.Spaces().AppendMessageWithRouting(detail.Item.ID, space.Message{
		AuthorID:    "coder",
		AuthorKind:  space.ParticipantAgent,
		Content:     "stale work",
		Status:      "pending",
		RuntimeMeta: map[string]string{pendingMetaStreamID: "stream-dead-process"},
		CreatedAt:   time.Now().Add(-pendingRecoveryGrace - time.Minute),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := b.GetAgentDM(detail.Item.ID)
	var found bool
	for _, m := range got.Messages {
		if m.ID != pending.ID {
			continue
		}
		found = true
		if m.Status != "failed" || m.Error == "" {
			t.Fatalf("expired pending = %#v, want failed retry placeholder", m)
		}
	}
	if !found {
		t.Fatalf("pending message %s missing: %#v", pending.ID, got.Messages)
	}

	sp, err := a.Spaces().LoadSpace(detail.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range sp.Messages {
		if m.ID == pending.ID && m.Status != "failed" {
			t.Fatalf("expired pending was not persisted as failed: %#v", m)
		}
	}
}

func TestRetrySupersededFailedMessageDoesNotDuplicateReply(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	detail := b.GetAgentDM("coder")
	if _, err := a.Spaces().AppendUserMessage(detail.Item.ID, "explain next steps", nil); err != nil {
		t.Fatal(err)
	}
	failed, _, err := a.Spaces().AppendMessageWithRouting(detail.Item.ID, space.Message{
		AuthorID:   "coder",
		AuthorKind: space.ParticipantAgent,
		Status:     "failed",
		Error:      "stale retry placeholder",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(detail.Item.ID, space.Message{
		AuthorID:   "coder",
		AuthorKind: space.ParticipantAgent,
		Content:    "real successful answer",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := b.RetryMessage(RetryMessageRequest{SpaceID: detail.Item.ID, MessageID: failed.ID}); err == nil {
		t.Fatal("retry should reject stale failed placeholder after a successful reply")
	}
	got := b.GetAgentDM(detail.Item.ID)
	var users, successfulMessages, failedMessages int
	for _, m := range got.Messages {
		if m.Role == "user" {
			users++
		}
		if m.Role == "agent" && m.Content == "real successful answer" {
			successfulMessages++
		}
		if m.Status == "failed" || m.Status == "pending" {
			failedMessages++
		}
	}
	if users != 1 || successfulMessages != 1 || failedMessages != 0 {
		t.Fatalf("messages after stale retry = users:%d success:%d failed:%d messages=%#v", users, successfulMessages, failedMessages, got.Messages)
	}
}

func TestDeleteMissingSpaceIDIsIdempotent(t *testing.T) {
	b, a := newBackendWithApp(t)
	staleID := "20260612-agent-10ff4668"

	res, err := b.DeleteConversation(DeleteConversationRequest{Kind: "agent_dm", ID: staleID})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.DeletedSpace || res.DeletedSessions != 0 || res.DeletedTasks != 0 {
		t.Fatalf("delete result = %+v, want idempotent ok with no deletions", res)
	}
	spaces, err := a.Spaces().ListSpaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range spaces {
		if sp.ID == staleID || sp.Kind == space.KindAgentDM {
			t.Fatalf("idempotent delete should not create spaces, got %#v", sp)
		}
	}
}

func TestDeleteThreadRemovesRepliesThreadSessionsAndTasks(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	root, err := a.Spaces().AppendUserMessage(sp.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendAgentMessage(sp.ID, space.PersonaInfo{ID: "coder", Display: "Coder"}, "reply", "", nil, root.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Spaces().SetThreadAgentMode(sp.ID, root.ID, "coder", "listen"); err != nil {
		t.Fatal(err)
	}
	source := "desktop:channel:" + sp.ID + ":thread:" + root.ID + ":persona:coder"
	sess, err := a.CurrentSession(source)
	if err != nil {
		t.Fatal(err)
	}
	sess.Add(msg.Message{Role: "user", Content: "thread memory"})
	if err := a.SaveSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          sp.ID,
		TriggerMessageID: "reply-1",
		SourceThreadID:   root.ID,
		InitiatorID:      "user",
		WorkerID:         "coder",
		Title:            "thread task",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := b.DeleteConversation(DeleteConversationRequest{Kind: "thread", ID: sp.ID, ParentMessageID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.DeletedSpace || res.DeletedSessions != 1 || res.DeletedTasks != 1 {
		t.Fatalf("delete thread result = %+v", res)
	}
	updated, err := a.Spaces().LoadSpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Messages) != 1 || updated.Messages[0].ID != root.ID {
		t.Fatalf("messages after thread delete = %#v, want only root", updated.Messages)
	}
	if _, ok := updated.ThreadAgentModes[root.ID]; ok {
		t.Fatalf("thread agent modes still contain deleted thread: %#v", updated.ThreadAgentModes)
	}
	if sessions, err := a.ListSessions(); err != nil {
		t.Fatal(err)
	} else if len(sessions) != 0 {
		t.Fatalf("sessions after thread delete = %#v, want empty", sessions)
	}
	if tasks, err := a.Tasks().ListBySpace(sp.ID); err != nil {
		t.Fatal(err)
	} else if len(tasks) != 0 {
		t.Fatalf("tasks after thread delete = %#v, want empty", tasks)
	}
}

func TestParseTaskID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"scheduled next team turn for coder, task_id=task-abc123", "task-abc123"},
		{"foo task_id=task-xyz bar", "task-xyz"},
		{"no task here", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := parseTaskID(c.in)
		if got != c.want {
			t.Errorf("parseTaskID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

type desktopRuntimeFunc func(context.Context, *agent.Turn) error

func (f desktopRuntimeFunc) Run(ctx context.Context, turn *agent.Turn) error {
	return f(ctx, turn)
}

func TestHumanizeStep(t *testing.T) {
	ws := "/Users/me/repo"
	cases := []struct {
		tool string
		args string
		want string
	}{
		{"read", `{"path":"/Users/me/repo/llm/anthropic.go"}`, "read llm/anthropic.go"},
		{"list_files", `{"path":"/Users/me/repo/plugins/collab"}`, "listed plugins/collab/"},
		{"bash", `{"cmd":"ls /Users/me/repo/cmd"}`, "ran ls cmd"},
		{"bash", `{"cmd":"go test ./..."}`, "ran go test ./..."},
		{"grep", `{"query":"retry behaviour"}`, "searched for retry behaviour"},
		{"read", `{"path":"/etc/passwd"}`, "read passwd"},
		{"unknown_tool", `{}`, "unknown_tool"},
	}
	for _, c := range cases {
		got := humanizeStep(c.tool, c.args, ws)
		if got != c.want {
			t.Errorf("humanizeStep(%s, %s) = %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}
