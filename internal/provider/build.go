package provider

// Shared helpers for building provider-specific request payloads.
// Both AnthropicMessages and OpenAIResponses implement the Client interface
// and share the same message/tool conversion patterns.

func buildToolDefs(tools []Tool, fn func(Tool) any) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, fn(t))
	}
	return out
}
