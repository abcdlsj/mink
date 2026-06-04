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
