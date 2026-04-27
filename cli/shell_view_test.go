package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderHeaderMatchesCodexShell(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 32

	out := ansi.Strip(m.renderHeader())
	lines := strings.Split(out, "\n")
	if len(lines) < 6 {
		t.Fatalf("renderHeader() lines = %d, want at least 6", len(lines))
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
