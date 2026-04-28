package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/abcdlsj/mink/bus"
	tea "github.com/charmbracelet/bubbletea"
)

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
