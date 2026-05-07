package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/session"
	tea "github.com/charmbracelet/bubbletea"
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

func TestMouseWheelScrollsTranscriptWhileComposerFocused(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 40
	m.height = 12
	m.syncLayout()
	for i := 0; i < 24; i++ {
		m.addItem(chatItem{
			Kind: itemAssistant,
			Segments: []chatSegment{{
				Kind: segText,
				Text: fmt.Sprintf("line %02d", i),
			}},
		})
	}
	if m.focus != focusComposer {
		t.Fatalf("focus = %v, want composer", m.focus)
	}
	before := m.viewport.YOffset
	if before == 0 {
		t.Fatal("viewport did not start at bottom")
	}

	next, _ := m.Update(tea.MouseMsg{
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	got := next.(shellModel)
	if got.viewport.YOffset >= before {
		t.Fatalf("YOffset = %d, want less than %d", got.viewport.YOffset, before)
	}
	if got.focus != focusComposer {
		t.Fatalf("focus = %v, want composer", got.focus)
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
