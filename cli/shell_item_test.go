package cli

import (
	"testing"
	"time"
)

func TestSummarizeToolActionUsesParsedFields(t *testing.T) {
	got := summarizeToolAction("bash", `{"cmd":"printf 你好"}`)
	if got != "Run bash printf 你好" {
		t.Fatalf("summarizeToolAction() = %q", got)
	}
}

func TestSummarizeToolOutputUsesSafePreview(t *testing.T) {
	got := summarizeToolOutput("你好吗世界和平")
	if got == "" {
		t.Fatal("expected preview")
	}
}

func TestAppendReasoningSeparatesPhases(t *testing.T) {
	item := chatItem{Kind: itemAssistant}
	item.appendReasoning("Let me check first.")
	item.addTool("bash", "Run bash pwd", "", time.Time{})
	item.appendReasoning("There")
	item.appendReasoning("'s a workspace.")

	if len(item.Segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(item.Segments))
	}
	if got, want := item.Segments[0].Text, "Let me check first."; got != want {
		t.Fatalf("first reasoning = %q, want %q", got, want)
	}
	if got, want := item.Segments[2].Text, "There's a workspace."; got != want {
		t.Fatalf("second reasoning = %q, want %q", got, want)
	}
}

func TestAppendReasoningKeepsStreamingDeltasTight(t *testing.T) {
	item := chatItem{Kind: itemAssistant}
	item.appendReasoning("Let")
	item.appendReasoning(" me")
	item.appendReasoning(" check")

	if got, want := item.Segments[0].Text, "Let me check"; got != want {
		t.Fatalf("reasoning = %q, want %q", got, want)
	}
}
