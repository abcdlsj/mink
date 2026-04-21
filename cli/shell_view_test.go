package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderHeaderIncludesDivider(t *testing.T) {
	m := newShellModel(context.Background(), nil, "cli")
	m.width = 32

	out := ansi.Strip(m.renderHeader())
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("renderHeader() lines = %d, want at least 2", len(lines))
	}
	if !strings.Contains(lines[0], "Mink") {
		t.Fatalf("renderHeader() first line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "─") {
		t.Fatalf("renderHeader() second line = %q, want divider", lines[1])
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
	if len(lines) < 4 {
		t.Fatalf("renderItem() lines = %d, want at least 4", len(lines))
	}
	if lines[1] != "" {
		t.Fatalf("renderItem() gap line = %q, want empty line", lines[1])
	}
	if !strings.Contains(lines[2], "你是什么模型") {
		t.Fatalf("renderItem() body line = %q", lines[2])
	}
}
