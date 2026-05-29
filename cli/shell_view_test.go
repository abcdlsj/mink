package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

type fakeShellApp struct{}

func (fakeShellApp) HandleInput(context.Context, string, string) (string, error) { return "", nil }
func (fakeShellApp) HandleInputWithAttachments(context.Context, string, string, []msg.Attachment) (string, error) {
	return "", nil
}
func (fakeShellApp) Config() config.Config { return config.Config{} }
func (fakeShellApp) CurrentModel() string  { return "claude-sonnet-4-6 with a long suffix" }
func (fakeShellApp) Commands() []command.Command {
	return []command.Command{
		command.NewFuncCmd("help", "show help", nil),
		command.NewFuncCmd("session", "manage sessions", nil),
	}
}
func (fakeShellApp) Workspace() string {
	return "/Users/lisongjian/Workspace/gh/abcdlsj/sumi/very/long/path"
}
func (fakeShellApp) Personas() *persona.Registry {
	return persona.NewRegistry("")
}
func (fakeShellApp) CurrentSession(string) (*session.Session, error) { return session.New("cli"), nil }
func (fakeShellApp) NewSession(string) (*session.Session, error)     { return session.New("cli"), nil }
func (fakeShellApp) DraftSession(string) *session.Session            { return session.New("cli") }
func (fakeShellApp) SwitchSession(string, string) (*session.Session, error) {
	return session.New("cli"), nil
}
func (fakeShellApp) ListSessionsBySource(string) ([]*session.Session, error) {
	return []*session.Session{session.New("cli")}, nil
}
func (fakeShellApp) Spaces() *space.Manager { return nil }

func TestRenderHeaderMatchesCodexShell(t *testing.T) {
	m := newShellModel(context.Background(), fakeShellApp{}, "cli")
	m.width = 96

	out := ansi.Strip(m.renderHeader(m.state()))
	lines := strings.Split(out, "\n")
	if len(lines) != shellHeaderHeight {
		t.Fatalf("renderHeader() lines = %d, want %d:\n%s", len(lines), shellHeaderHeight, out)
	}
	if strings.ContainsAny(out, "╭╮╰╯─│") {
		t.Fatalf("renderHeader() = %q, want no border", out)
	}
	if !strings.Contains(out, "Sumi") {
		t.Fatalf("renderHeader() = %q, want title", out)
	}
	if !strings.Contains(out, "model") || !strings.Contains(out, "cwd") || !strings.Contains(out, "-cli-") {
		t.Fatalf("renderHeader() = %q, want session facts", out)
	}
	for _, line := range lines {
		if w := runewidth.StringWidth(line); w > m.width {
			t.Fatalf("header line width = %d, want <= %d: %q\n%s", w, m.width, line, out)
		}
	}
}

func TestRenderHeaderKeepsSessionOnNarrowWidth(t *testing.T) {
	m := newShellModel(context.Background(), fakeShellApp{}, "cli")
	m.width = 32

	out := ansi.Strip(m.renderHeader(m.state()))
	if !strings.Contains(out, "-cli-") {
		t.Fatalf("renderHeader() = %q, want session visible", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if w := runewidth.StringWidth(line); w > m.width {
			t.Fatalf("header line width = %d, want <= %d: %q\n%s", w, m.width, line, out)
		}
	}
}

func TestViewKeepsFullHeaderVisible(t *testing.T) {
	m := newShellModel(context.Background(), fakeShellApp{}, "cli")
	m.width = 32
	m.height = 10
	m.syncLayout()

	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if len(lines) < shellHeaderHeight {
		t.Fatalf("View() lines = %d, want header:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	head := strings.Join(lines[:shellHeaderHeight], "\n")
	for _, want := range []string{"Sumi", "-cli-"} {
		if !strings.Contains(head, want) {
			t.Fatalf("header missing %q:\n%s", want, head)
		}
	}
	if strings.ContainsAny(head, "╭╮╰╯─│") {
		t.Fatalf("header has border:\n%s", head)
	}
	for i, line := range lines {
		if w := runewidth.StringWidth(line); w > m.width {
			t.Fatalf("View() line %d width = %d, want <= %d: %q\n%s", i, w, m.width, line, strings.Join(lines, "\n"))
		}
	}
}

func TestViewKeepsHeaderFactsWhileTyping(t *testing.T) {
	m := newShellModel(context.Background(), fakeShellApp{}, "cli")
	m.width = 32
	m.height = 10
	m.input.SetValue("this is a long enough prompt to wrap in the composer")
	m.syncLayout()

	requireHeaderFacts(t, m, "while typing")
}

func TestViewKeepsHeaderFactsWithSuggestions(t *testing.T) {
	m := newShellModel(context.Background(), fakeShellApp{}, "cli")
	m.width = 32
	m.height = 10
	m.input.SetValue("/")
	m.syncLayout()

	requireHeaderFacts(t, m, "with suggestions")
}

func TestTranscriptUsesSameOuterIndent(t *testing.T) {
	m := newShellModel(context.Background(), fakeShellApp{}, "cli")
	m.width = 48
	m.height = 10
	m.syncLayout()
	m.addTextItem(itemUser, "你是什么模型", time.Now())

	out := ansi.Strip(m.renderTranscript())
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("transcript line missing outer indent: %q\n%s", line, out)
		}
		break
	}
}

func TestRenderItemKeepsUserMessageCompact(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 48
	item := &chatItem{
		Kind: itemUser,
		Time: time.Date(2026, 4, 21, 14, 48, 1, 0, time.Local),
		Segments: []chatSegment{{
			Kind: segText,
			Text: "你是什么模型",
		}},
	}

	lines := m.renderItem(item, 0)
	if len(lines) != 1 {
		t.Fatalf("renderItem() lines = %d, want 1: %#v", len(lines), lines)
	}
	if !strings.Contains(ansi.Strip(lines[0]), "› 你是什么模型") {
		t.Fatalf("renderItem() body line = %q", lines[0])
	}
}

func TestMainConversationHidesMessageIDs(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 80
	user := &chatItem{ID: "m01", Kind: itemUser, Segments: []chatSegment{{Kind: segText, Text: "hello"}}}
	assistant := &chatItem{ID: "m02", Kind: itemAssistant, Segments: []chatSegment{{Kind: segText, Text: "world"}}}
	m.items = []*chatItem{user, assistant}

	out := ansi.Strip(strings.Join(append(m.renderItem(user, 0), m.renderItem(assistant, 1)...), "\n"))
	if strings.Contains(out, "m01") || strings.Contains(out, "m02") {
		t.Fatalf("main conversation leaked ids:\n%s", out)
	}
}

func TestThreadConversationShowsMessageIDs(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 80
	m.thread = &shellThread{ID: "t1", Title: "thread"}
	user := &chatItem{ID: "m01", Kind: itemUser, Segments: []chatSegment{{Kind: segText, Text: "hello"}}}
	assistant := &chatItem{ID: "m02", Kind: itemAssistant, Segments: []chatSegment{{Kind: segText, Text: "world"}}}
	m.items = []*chatItem{user, assistant}

	out := ansi.Strip(strings.Join(append(m.renderItem(user, 0), m.renderItem(assistant, 1)...), "\n"))
	for _, want := range []string{"m01", "m02"} {
		if !strings.Contains(out, want) {
			t.Fatalf("thread output missing %q:\n%s", want, out)
		}
	}
}

func requireHeaderFacts(t *testing.T, m shellModel, note string) {
	t.Helper()
	out := ansi.Strip(m.View())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > m.height {
		t.Fatalf("View() lines = %d, want <= %d %s:\n%s", len(lines), m.height, note, out)
	}
	head := strings.Join(lines[:shellHeaderHeight], "\n")
	for _, want := range []string{"Sumi", "-cli-"} {
		if !strings.Contains(head, want) {
			t.Fatalf("header missing %q %s:\n%s", want, note, head)
		}
	}
}

func TestRenderItemAddsSingleGapBetweenSegments(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 64
	item := &chatItem{
		Kind: itemAssistant,
		Segments: []chatSegment{
			{Kind: segTool, Tool: "bash", Text: "pwd", Status: "done"},
			{Kind: segText, Text: "done"},
		},
	}

	lines := ansi.Strip(strings.Join(m.renderItem(item, 0), "\n"))
	if strings.Contains(lines, "ran") || strings.Contains(lines, "bash") {
		t.Fatalf("completed tool leaked into assistant body:\n%s", lines)
	}
	if !strings.Contains(lines, "done") {
		t.Fatalf("assistant text missing:\n%s", lines)
	}
}

func TestRenderAssistantKeepsFailedToolsOutOfBody(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 80
	item := &chatItem{
		Kind: itemAssistant,
		Segments: []chatSegment{
			{Kind: segReasoning, Text: "先想"},
			{Kind: segTool, Tool: "read", Text: "read self", Status: "failed", Detail: "no file"},
			{Kind: segText, Text: "看起来我的自我目录尚未初始化。"},
			{Kind: segTool, Tool: "bash", Text: "mkdir self", Status: "failed", Detail: "denied"},
			{Kind: segText, Text: "你好！我是 Sumi。"},
		},
	}

	out := ansi.Strip(strings.Join(m.renderItem(item, 0), "\n"))
	if strings.Contains(out, "failed") || strings.Contains(out, "read self") || strings.Contains(out, "mkdir self") {
		t.Fatalf("failed tool leaked into assistant body:\n%s", out)
	}
	if !strings.Contains(out, "看起来我的自我目录尚未初始化。你好！我是 Sumi。") {
		t.Fatalf("assistant text was not kept together:\n%s", out)
	}
}

func TestRenderAssistantJoinsReasoningPhasesOnce(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 80
	item := &chatItem{
		Kind: itemAssistant,
		Segments: []chatSegment{
			{Kind: segReasoning, Text: "Let me check first."},
			{Kind: segTool, Tool: "bash", Text: "pwd", Status: "done"},
			{Kind: segReasoning, Text: "There's a workspace."},
		},
	}

	out := ansi.Strip(strings.Join(m.renderItem(item, 0), "\n"))
	if strings.Count(out, "Thinking") != 1 {
		t.Fatalf("thinking header count wrong:\n%s", out)
	}
	if !strings.Contains(out, "Let me check first.\n  \n  There's a workspace.") {
		t.Fatalf("reasoning phases not separated cleanly:\n%s", out)
	}
}

func TestSelectedToolLineDoesNotPaintTrailingBlock(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.viewport.Width = 64
	line := m.renderToolLine(chatSegment{Tool: "bash", Text: "pwd", Status: "done"}, true)

	if strings.Contains(line, "\x1b[48;5;235m") {
		t.Fatalf("selected tool line has background block: %q", line)
	}
}
