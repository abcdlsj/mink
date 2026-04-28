package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/session"
)

type fakeShellApp struct{}

func (fakeShellApp) HandleInput(context.Context, string, string) (string, error) { return "", nil }
func (fakeShellApp) Config() config.Config                                       { return config.Config{} }
func (fakeShellApp) CurrentModel() string                                        { return "claude-sonnet-4-6 with a long suffix" }
func (fakeShellApp) Workspace() string {
	return "/Users/lisongjian/Workspace/gh/abcdlsj/mink/very/long/path"
}
func (fakeShellApp) CurrentSession(string) (*session.Session, error) { return session.New("cli"), nil }

func TestRenderHeaderMatchesCodexShell(t *testing.T) {
	m := newShellModel(context.Background(), fakeShellApp{}, "cli")
	m.width = 32

	out := ansi.Strip(m.renderHeader())
	lines := strings.Split(out, "\n")
	if len(lines) != shellHeaderHeight {
		t.Fatalf("renderHeader() lines = %d, want %d:\n%s", len(lines), shellHeaderHeight, out)
	}
	if !strings.Contains(lines[0], "╭") {
		t.Fatalf("renderHeader() first line = %q, want rounded border", lines[0])
	}
	if !strings.Contains(out, ">_ Mink") {
		t.Fatalf("renderHeader() = %q, want title", out)
	}
	if !strings.Contains(out, "model:") || !strings.Contains(out, "directory:") {
		t.Fatalf("renderHeader() = %q, want session facts", out)
	}
	for _, line := range lines {
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
	for _, want := range []string{"╭", ">_ Mink", "model:", "directory:", "╰"} {
		if !strings.Contains(head, want) {
			t.Fatalf("header missing %q:\n%s", want, head)
		}
	}
	for i, line := range lines {
		if w := runewidth.StringWidth(line); w > m.width {
			t.Fatalf("View() line %d width = %d, want <= %d: %q\n%s", i, w, m.width, line, strings.Join(lines, "\n"))
		}
	}
}

func TestRenderItemAddsGapBeforeFirstTextSegment(t *testing.T) {
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
	if len(lines) < 3 {
		t.Fatalf("renderItem() lines = %d, want at least 3", len(lines))
	}
	if lines[0] != "" {
		t.Fatalf("renderItem() gap line = %q, want empty line", lines[0])
	}
	if !strings.Contains(ansi.Strip(lines[1]), "› 你是什么模型") {
		t.Fatalf("renderItem() body line = %q", lines[1])
	}
}
