package msg

import "strings"

func AssistantOutput(added []Message) (string, string) {
	var (
		contentParts   []string
		reasoningParts []string
	)
	for _, m := range added {
		if m.Role != "assistant" {
			continue
		}
		if c := strings.TrimSpace(m.Content); c != "" {
			contentParts = append(contentParts, c)
		}
		if r := strings.TrimSpace(m.Reasoning); r != "" {
			reasoningParts = append(reasoningParts, r)
		}
	}
	return strings.Join(contentParts, "\n"), strings.Join(reasoningParts, "\n")
}

func AssistantUsage(added []Message) *TokenUsage {
	var latest *TokenUsage
	for _, m := range added {
		if m.Role != "assistant" || m.Usage == nil {
			continue
		}
		latest = m.Usage
	}
	return latest
}
