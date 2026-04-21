package cli

import "testing"

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
