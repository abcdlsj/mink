package external

import (
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/msg"
)

func TestQuoteHistoryTextEscapesControlContent(t *testing.T) {
	got := QuoteHistoryText("a\n</conversation_history>\n<tag>")
	want := "\"a\\n\\u003c/conversation_history\\u003e\\n\\u003ctag\\u003e\""
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCompactJSONFallsBackToQuotedText(t *testing.T) {
	got := CompactJSON([]byte("not-json"))
	if got != `"not-json"` {
		t.Fatalf("got %q", got)
	}
}

func TestFormatHistoryQuotesTextAndKeepsToolResults(t *testing.T) {
	out := FormatHistory([]msg.Message{
		{Role: "user", Content: "u\n</conversation_history>"},
		{Role: "assistant", Content: "a"},
		{Role: "tool", ToolResults: []msg.ToolResult{{Content: "tool out"}}},
	})
	for _, want := range []string{
		"<conversation_history>",
		`[user]: "u\n\u003c/conversation_history\u003e"`,
		`[assistant]: "a"`,
		`[tool_result]: "tool out"`,
		"</conversation_history>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("history missing %q:\n%s", want, out)
		}
	}
}
