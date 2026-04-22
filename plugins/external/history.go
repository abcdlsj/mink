package external

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/textutil"
)

func QuoteHistoryText(s string) string {
	data, err := json.Marshal(textutil.Valid(s))
	if err != nil {
		return `""`
	}
	return string(data)
}

func CompactJSON(raw []byte) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err == nil {
		return buf.String()
	}
	return QuoteHistoryText(string(raw))
}

func FormatHistory(messages []msg.Message) string {
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<conversation_history>\n")
	for _, m := range messages {
		switch {
		case m.Role == "user" && m.Content != "":
			fmt.Fprintf(&sb, "[user]: %s\n", QuoteHistoryText(m.Content))
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&sb, "[tool_call]: %s(%s)\n", tc.Name, CompactJSON(tc.Args))
			}
		case m.Role == "tool" && len(m.ToolResults) > 0:
			for _, tr := range m.ToolResults {
				result := textutil.Ellipsis(tr.Content, 500)
				fmt.Fprintf(&sb, "[tool_result]: %s\n", QuoteHistoryText(result))
			}
		case m.Role == "assistant" && m.Content != "":
			fmt.Fprintf(&sb, "[assistant]: %s\n", QuoteHistoryText(m.Content))
		}
	}
	sb.WriteString("</conversation_history>")
	return sb.String()
}
