package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
)

type commandShellApp struct {
	workspace string
}

func (commandShellApp) HandleInput(context.Context, string, string) (string, error) { return "ok", nil }
func (commandShellApp) Config() config.Config                                       { return config.Config{} }
func (commandShellApp) CurrentModel() string                                        { return "test-model" }
func (commandShellApp) Commands() []command.Command {
	return []command.Command{
		command.NewFuncCmd("compact", "compact current session", nil),
		command.NewFuncCmd("help", "show help", nil),
		command.NewFuncCmd("session", "manage sessions", nil),
	}
}
func (a commandShellApp) Workspace() string {
	if a.workspace != "" {
		return a.workspace
	}
	return "."
}
func (commandShellApp) Personas() *persona.Registry {
	return persona.NewRegistry("")
}
func (commandShellApp) CurrentSession(string) (*session.Session, error) {
	return session.New("cli"), nil
}
func (commandShellApp) NewSession(string) (*session.Session, error) {
	return session.New("cli"), nil
}
func (commandShellApp) SwitchSession(string, string) (*session.Session, error) {
	return session.New("cli"), nil
}
func (commandShellApp) ListSessionsBySource(string) ([]*session.Session, error) {
	return []*session.Session{session.New("cli")}, nil
}

func TestShellModelGroupsToolIntoAssistantTurn(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.turn = shellTurn{assistantIndex: -1}

	m.handleEvent(bus.Event{
		Type:       bus.ToolCallStarted,
		Source:     "cli",
		ToolCallID: "tool-1",
		Tool:       "bash",
		Input:      `{"cmd":"pwd"}`,
	})
	m.handleEvent(bus.Event{
		Type:       bus.ToolCallFinished,
		Source:     "cli",
		ToolCallID: "tool-1",
		Tool:       "bash",
		Input:      `{"cmd":"pwd"}`,
		Output:     "/tmp/project",
	})
	m.appendAssistant("done")

	if len(m.items) != 1 {
		t.Fatalf("items = %d, want 1", len(m.items))
	}
	item := m.items[0]
	if item.Kind != itemAssistant {
		t.Fatalf("kind = %d", item.Kind)
	}
	if len(item.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(item.Segments))
	}
	if item.Segments[0].Kind != segTool {
		t.Fatalf("segment[0].Kind = %d, want tool", item.Segments[0].Kind)
	}
	if item.Segments[1].Kind != segText {
		t.Fatalf("segment[1].Kind = %d, want text", item.Segments[1].Kind)
	}
}

func TestShellModelIgnoresLateDuplicateChunk(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.turn = shellTurn{assistantIndex: -1}

	m.appendAssistant("done")
	m.turn = shellTurn{assistantIndex: -1}
	m.handleEvent(bus.Event{Type: bus.TurnChunk, Source: "cli", Text: "done"})
	m.handleEvent(bus.Event{Type: bus.TurnChunk, Source: "cli", Text: "one"})

	if len(m.items) != 1 {
		t.Fatalf("items = %d, want 1", len(m.items))
	}
	if got := assistantText(m.items[0]); got != "done" {
		t.Fatalf("assistant text = %q, want done", got)
	}
}

func TestShellModelHandlesBatchedStreamEventsInOrder(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 80
	m.height = 24
	m.syncLayout()

	m.handleEvents([]bus.Event{
		{Type: bus.TurnChunk, Source: "cli", Text: "调用方：`mobile.app-im.im"},
		{Type: bus.TurnChunk, Source: "cli", Text: "-interface` 调用触发，在 "},
		{Type: bus.TurnChunk, Source: "cli", Text: "`service_dyn_intra.go:253`。"},
	})

	if len(m.items) != 1 {
		t.Fatalf("items = %d, want 1", len(m.items))
	}
	want := "调用方：`mobile.app-im.im-interface` 调用触发，在 `service_dyn_intra.go:253`。"
	if got := assistantText(m.items[0]); got != want {
		t.Fatalf("assistant text = %q, want %q", got, want)
	}
	if m.deferSync || m.syncDirty {
		t.Fatalf("sync flags left dirty: defer=%v dirty=%v", m.deferSync, m.syncDirty)
	}
}

func TestShellModelReconcilesStreamedAssistantSnapshot(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 80
	m.height = 24
	m.syncLayout()

	m.handleEvent(bus.Event{Type: bus.TurnChunk, Source: "cli", Text: "调用方：`mobile.app-im.im"})
	m.handleEvent(bus.Event{Type: bus.TurnChunk, Source: "cli", Text: " 调用触发"})
	m.turn.streamed = true
	m.finishTurn("调用方：`mobile.app-im.im-interface` 调用触发", nil)

	if len(m.items) != 1 {
		t.Fatalf("items = %d, want 1", len(m.items))
	}
	want := "调用方：`mobile.app-im.im-interface` 调用触发"
	if got := assistantText(m.items[0]); got != want {
		t.Fatalf("assistant text = %q, want %q", got, want)
	}
}

func TestAppendEventMergesConsecutiveStreamEvents(t *testing.T) {
	evs := appendEvent(nil, bus.Event{Type: bus.TurnChunk, Source: "cli", Text: "a"})
	evs = appendEvent(evs, bus.Event{Type: bus.TurnChunk, Source: "cli", Text: "b"})
	evs = appendEvent(evs, bus.Event{Type: bus.TurnReasoning, Source: "cli", Text: "c"})
	evs = appendEvent(evs, bus.Event{Type: bus.TurnReasoning, Source: "cli", Text: "d"})

	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2: %#v", len(evs), evs)
	}
	if evs[0].Text != "ab" || evs[1].Text != "cd" {
		t.Fatalf("merged texts = %q, %q", evs[0].Text, evs[1].Text)
	}
}

func TestViewportMouseWheelIsHandledByShell(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	if m.viewport.MouseWheelEnabled {
		t.Fatal("viewport mouse wheel should stay disabled; shell handles wheel events")
	}
}

func TestMouseWheelScrollsTranscript(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 80
	m.height = 10
	m.syncLayout()
	for i := 0; i < 20; i++ {
		m.addTextItem(itemNotice, fmt.Sprintf("line %02d", i), time.Now())
	}
	m.viewport.SetYOffset(0)

	next, _ := m.updateMouse(tea.MouseMsg(tea.MouseEvent{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	}))
	got := next.(shellModel)
	if got.viewport.YOffset == 0 {
		t.Fatal("wheel down should scroll transcript")
	}
	if got.follow {
		t.Fatal("wheel scroll should disable follow mode")
	}
}

func TestMouseWheelAtBottomKeepsFollow(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 80
	m.height = 10
	m.syncLayout()
	for i := 0; i < 20; i++ {
		m.addTextItem(itemNotice, fmt.Sprintf("line %02d", i), time.Now())
	}
	m.follow = true
	y := m.viewport.YOffset

	next, _ := m.updateMouse(tea.MouseMsg(tea.MouseEvent{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	}))
	got := next.(shellModel)
	if got.viewport.YOffset != y {
		t.Fatalf("offset = %d, want %d", got.viewport.YOffset, y)
	}
	if !got.follow {
		t.Fatal("wheel down at bottom should keep follow mode")
	}
}

func TestMouseMotionDoesNotScrollTranscript(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 80
	m.height = 10
	m.syncLayout()
	for i := 0; i < 20; i++ {
		m.addTextItem(itemNotice, fmt.Sprintf("line %02d", i), time.Now())
	}
	m.viewport.SetYOffset(0)

	next, _ := m.updateMouse(tea.MouseMsg(tea.MouseEvent{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonWheelDown,
	}))
	got := next.(shellModel)
	if got.viewport.YOffset != 0 {
		t.Fatalf("motion wheel event changed offset to %d", got.viewport.YOffset)
	}
}

func TestStreamEventsThrottleViewportSync(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 80
	m.height = 24
	m.syncLayout()
	m.lastSync = time.Now()

	cmd := m.handleEvents([]bus.Event{
		{Type: bus.TurnChunk, Source: "cli", Text: "stream"},
	})
	if cmd == nil {
		t.Fatal("stream event should schedule a delayed sync")
	}
	if !m.syncPending || !m.syncDirty {
		t.Fatalf("sync state = pending:%v dirty:%v, want pending and dirty", m.syncPending, m.syncDirty)
	}
	if got := assistantText(m.items[0]); got != "stream" {
		t.Fatalf("assistant text = %q, want stream", got)
	}

	next, _ := m.Update(shellSyncMsg{})
	got := next.(shellModel)
	if got.syncPending || got.syncDirty {
		t.Fatalf("sync state after tick = pending:%v dirty:%v", got.syncPending, got.syncDirty)
	}
}

func TestSlashCommandSuggestionsCanBeAccepted(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.width = 80
	m.height = 24
	m.input.SetValue("/he")
	m.syncLayout()

	if len(m.suggests) != 1 || m.suggests[0].Value != "help" {
		t.Fatalf("suggests = %#v, want help", m.suggests)
	}
	m.acceptSuggestion()
	if got := m.input.Value(); got != "/help " {
		t.Fatalf("input = %q, want /help ", got)
	}
}

func TestSubmitQueuesWhileBusy(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.busy = true
	m.input.SetValue("second task")

	next, cmd := m.submit()
	got := next.(shellModel)
	if cmd != nil {
		t.Fatal("busy submit returned command")
	}
	if len(got.queue) != 1 || got.queue[0] != "second task" {
		t.Fatalf("queue = %#v", got.queue)
	}
	if got.input.Value() != "" {
		t.Fatalf("input = %q, want empty", got.input.Value())
	}
}

func TestSubmitStripsTerminalStatusResponses(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.busy = true
	m.input.SetValue("\x1b]11;rgb:1515/1111/1010\x1b\\]11;rgb:1515/1111/1010\\你好\x1b[24;1R")

	next, cmd := m.submit()
	got := next.(shellModel)
	if cmd != nil {
		t.Fatal("busy submit returned command")
	}
	if len(got.queue) != 1 || got.queue[0] != "你好" {
		t.Fatalf("queue = %#v, want 你好", got.queue)
	}
}

type cancelShellApp struct {
	commandShellApp
	started chan struct{}
}

func (a cancelShellApp) HandleInput(ctx context.Context, source, input string) (string, error) {
	close(a.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestEscInterruptsRunningTurn(t *testing.T) {
	app := cancelShellApp{started: make(chan struct{})}
	m := newShellModel(context.Background(), app, "cli")
	m.input.SetValue("long task")

	next, cmd := m.submit()
	m = next.(shellModel)
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-app.started

	next, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(shellModel)
	if !m.turn.interrupted {
		t.Fatal("turn should be marked interrupted")
	}

	msg := (<-done).(shellTurnDoneMsg)
	if !errors.Is(msg.Err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", msg.Err)
	}

	m.finishTurn(msg.Reply, msg.Err)
	if len(m.items) != 1 || m.items[0].Kind != itemUser {
		t.Fatalf("items = %#v, want user item only", m.items)
	}
}

func TestAtFileSuggestionsCanBeAccepted(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli", "shell_model.go"), []byte("package cli\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "bad.go"), []byte("package bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := commandShellApp{workspace: dir}
	m := newShellModel(context.Background(), app, "cli")
	m.width = 80
	m.height = 24
	m.files = listWorkspaceFiles(dir)
	m.filesOK = true
	m.input.SetValue("read @files:shell")
	m.syncLayout()

	if len(m.suggests) != 1 || m.suggests[0].Value != "cli/shell_model.go" {
		t.Fatalf("suggests = %#v", m.suggests)
	}
	m.acceptSuggestion()
	if got := m.input.Value(); got != "read @cli/shell_model.go " {
		t.Fatalf("input = %q", got)
	}
}

func TestAtMentionStartsWithSourceSuggestions(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.width = 80
	m.height = 24
	m.input.SetValue("@")
	m.syncLayout()

	if m.filesLoading || m.filesOK {
		t.Fatalf("files loaded before source selection: loading=%v ok=%v", m.filesLoading, m.filesOK)
	}
	if len(m.suggests) != 2 {
		t.Fatalf("suggests = %#v, want persona and files", m.suggests)
	}
	if m.suggests[0].Kind != completionMention || m.suggests[0].Value != "persona" {
		t.Fatalf("first suggest = %#v, want persona source", m.suggests[0])
	}
	if m.suggests[1].Kind != completionMention || m.suggests[1].Value != "files" {
		t.Fatalf("second suggest = %#v, want files source", m.suggests[1])
	}
}

func TestAtFilesSelectionLoadsFiles(t *testing.T) {
	dir := t.TempDir()
	app := commandShellApp{workspace: dir}
	m := newShellModel(context.Background(), app, "cli")
	m.width = 80
	m.height = 24
	m.input.SetValue("@")
	m.syncLayout()

	m.moveSuggestion(1)
	m.acceptSuggestion()
	m.syncLayout()

	if got := m.input.Value(); got != "@files:" {
		t.Fatalf("input = %q, want @files:", got)
	}
	if !m.filesLoading {
		t.Fatal("files should start loading after files source selection")
	}
	if m.pendingCmd == nil {
		t.Fatal("files source selection should queue load command")
	}

	next, _ := m.Update(shellFilesLoadedMsg{Root: dir, Files: []string{"cli/shell_model.go"}})
	got := next.(shellModel)
	if len(got.suggests) != 1 || got.suggests[0].Value != "cli/shell_model.go" {
		t.Fatalf("suggests after load = %#v", got.suggests)
	}
	if got.suggestRows == 0 {
		t.Fatal("suggest rows should be recalculated after files load")
	}
}

type personaShellApp struct {
	commandShellApp
	reg *persona.Registry
}

func (a *personaShellApp) Personas() *persona.Registry { return a.reg }

func TestAtPersonaSuggestionsList(t *testing.T) {
	dir := t.TempDir()
	reg := persona.NewRegistry(dir)
	if _, err := reg.Create("tshoot", persona.Meta{Display: "Tshoot", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create("reviewer", persona.Meta{Display: "Reviewer", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}

	m := newShellModel(context.Background(), &personaShellApp{reg: reg}, "cli")
	m.width = 80
	m.height = 24
	m.input.SetValue("@persona:")
	m.syncLayout()

	if len(m.suggests) != 2 {
		t.Fatalf("suggests = %#v", m.suggests)
	}
	if m.suggests[0].Kind != completionPersona || m.suggests[0].Value != "reviewer" {
		t.Fatalf("first suggest = %#v", m.suggests[0])
	}

	m.moveSuggestion(1)
	m.acceptSuggestion()
	if got := m.input.Value(); got != "@tshoot " {
		t.Fatalf("input = %q, want @tshoot ", got)
	}
	m.syncLayout()
	if len(m.suggests) != 0 {
		t.Fatalf("suggests after accept should be empty, got %#v", m.suggests)
	}
}

func TestRenderSuggestionsScrollsPastWindow(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.width = 60
	m.height = 20
	m.suggests = make([]completionHint, 10)
	for i := range m.suggests {
		m.suggests[i] = completionHint{Kind: completionFile, Value: fmt.Sprintf("f%02d.go", i)}
	}
	m.suggestRows = 6
	m.suggest = 9

	lines := m.renderSuggestions()
	if len(lines) != 6 {
		t.Fatalf("lines = %d", len(lines))
	}
	if !strings.Contains(lines[5], "f09.go") {
		t.Fatalf("last line missing f09.go: %q", lines[5])
	}
	if strings.Contains(lines[0], "f00.go") {
		t.Fatalf("window did not scroll: %q", lines[0])
	}
}

type sessionShellApp struct {
	commandShellApp
	sessions []*session.Session
	current  *session.Session
	switched string
	created  bool
}

func (a *sessionShellApp) CurrentSession(string) (*session.Session, error) {
	return a.current, nil
}

func (a *sessionShellApp) NewSession(src string) (*session.Session, error) {
	a.created = true
	s := session.New(src)
	s.Title = "new"
	a.current = s
	return s, nil
}

func (a *sessionShellApp) SwitchSession(_ string, id string) (*session.Session, error) {
	a.switched = id
	for _, s := range a.sessions {
		if s.ID == id {
			a.current = s
			return s, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (a *sessionShellApp) ListSessionsBySource(string) ([]*session.Session, error) {
	return a.sessions, nil
}

func TestSessionSelectorSwitchesSession(t *testing.T) {
	a := &sessionShellApp{}
	first := session.New("cli")
	first.Title = "first"
	second := session.New("cli")
	second.Title = "second"
	a.sessions = []*session.Session{first, second}
	a.current = first

	m := newShellModel(context.Background(), a, "cli")
	m.width = 80
	m.height = 24
	m.addTextItem(itemAssistant, "old transcript", time.Now())
	m.input.SetValue("/session")
	next, cmd := m.submit()
	if cmd != nil {
		t.Fatal("session selector submit returned command")
	}
	got := next.(shellModel)
	if got.overlay != overlaySession {
		t.Fatalf("overlay = %v, want session", got.overlay)
	}
	got.handleSessionKey("down")
	got.handleSessionKey("enter")

	if a.switched != second.ID {
		t.Fatalf("switched = %q, want %q", a.switched, second.ID)
	}
	if got.overlay != overlayNone {
		t.Fatalf("overlay = %v, want none", got.overlay)
	}
	if len(got.items) != 1 || got.items[0].Kind != itemNotice {
		t.Fatalf("items = %#v, want switch notice only", got.items)
	}
}

func TestSessionSelectorCanCreateSession(t *testing.T) {
	a := &sessionShellApp{}
	a.sessions = []*session.Session{session.New("cli")}
	a.current = a.sessions[0]
	m := newShellModel(context.Background(), a, "cli")
	m.openSessionOverlay()
	m.handleSessionKey("n")

	if !a.created {
		t.Fatal("new session was not created")
	}
	if m.overlay != overlayNone {
		t.Fatalf("overlay = %v, want none", m.overlay)
	}
}

func TestChannelCommandSwitchesSource(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.input.SetValue("/channel bugfix")

	next, cmd := m.submit()
	if cmd != nil {
		t.Fatal("channel command returned command")
	}
	got := next.(shellModel)
	if got.channel != "bugfix" {
		t.Fatalf("channel = %q", got.channel)
	}
	if got.source != "cli:channel:bugfix" {
		t.Fatalf("source = %q", got.source)
	}
	if len(got.items) != 1 || !strings.Contains(itemText(got.items[0]), "#bugfix") {
		t.Fatalf("items = %#v", got.items)
	}
}

func TestChannelCommandAcceptsHumanTitle(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.input.SetValue("/channel 排查 app-opus报错")

	next, cmd := m.submit()
	if cmd != nil {
		t.Fatal("channel command returned command")
	}
	got := next.(shellModel)
	if got.channel != "排查-app-opus报错" {
		t.Fatalf("channel = %q", got.channel)
	}
	if got.source != "cli:channel:排查-app-opus报错" {
		t.Fatalf("source = %q", got.source)
	}
	if len(got.items) != 1 || !strings.Contains(itemText(got.items[0]), "#排查-app-opus报错") {
		t.Fatalf("items = %#v", got.items)
	}
}

func TestThreadCommandCanOpenByMessageID(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	m.addTextItem(itemAssistant, "panic in cache", time.Now())
	id := m.items[0].ID
	m.input.SetValue("/thread " + id)

	next, cmd := m.submit()
	if cmd != nil {
		t.Fatal("thread command returned command")
	}
	got := next.(shellModel)
	if got.thread == nil || got.thread.ID != id {
		t.Fatalf("thread = %#v, want %s", got.thread, id)
	}
	if got.source != "cli:thread:"+id {
		t.Fatalf("source = %q", got.source)
	}
	if len(got.items) != 1 || !strings.Contains(itemText(got.items[0]), id) {
		t.Fatalf("items = %#v", got.items)
	}
}

func TestThreadLeaveReturnsToChannel(t *testing.T) {
	m := newShellModel(context.Background(), commandShellApp{}, "cli")
	th := m.createThread("panic", "m0a")
	m.enterThread(th)
	m.input.SetValue("/thread leave")

	next, _ := m.submit()
	got := next.(shellModel)
	if got.thread != nil {
		t.Fatalf("thread = %#v, want nil", got.thread)
	}
	if got.source != "cli" {
		t.Fatalf("source = %q", got.source)
	}
}
