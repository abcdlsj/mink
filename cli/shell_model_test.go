package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestMouseTrackingDisabledForCopy(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	if m.viewport.MouseWheelEnabled {
		t.Fatal("mouse wheel should be disabled so terminal selection works")
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
	m.input.SetValue("read @shell")
	m.syncLayout()

	if len(m.suggests) != 1 || m.suggests[0].Value != "cli/shell_model.go" {
		t.Fatalf("suggests = %#v", m.suggests)
	}
	m.acceptSuggestion()
	if got := m.input.Value(); got != "read @cli/shell_model.go " {
		t.Fatalf("input = %q", got)
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
