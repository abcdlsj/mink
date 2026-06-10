package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
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

func TestDefaultSumiPersonaSendFailurePersistsInputAndNotice(t *testing.T) {
	b, _ := newBackendWithApp(t)

	direct := b.ListDirectChats()
	if len(direct) != 1 {
		t.Fatalf("direct chats = %#v, want default Sumi", direct)
	}
	_, err := b.SendMessage(SendRequest{SessionID: direct[0].ID, PersonaID: "assistant", Input: "still there?"})
	if err == nil || err.Error() != "persona not found: assistant" {
		t.Fatalf("err = %v, want persona not found", err)
	}

	detail := b.GetDirectChat(direct[0].ID)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %#v, want user + persisted failure notice", detail.Messages)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Content != "still there?" {
		t.Fatalf("user message = %#v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "system" || detail.Messages[1].Content != "Send failed: persona not found: assistant" {
		t.Fatalf("failure notice = %#v", detail.Messages[1])
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
