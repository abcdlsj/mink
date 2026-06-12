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
	return NormalizeMarkdown(strings.Join(contentParts, "\n")), strings.Join(reasoningParts, "\n")
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

func AssistantRuntimeMeta(added []Message) map[string]string {
	var latest map[string]string
	for _, m := range added {
		if m.Role != "assistant" || len(m.RuntimeMeta) == 0 {
			continue
		}
		latest = m.RuntimeMeta
	}
	if len(latest) == 0 {
		return nil
	}
	out := make(map[string]string, len(latest))
	for k, v := range latest {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
