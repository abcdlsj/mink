package llm

import (
	"encoding/json"
	"strings"
)

func replayToolCallArgs(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	if json.Valid(raw) {
		return trimmed
	}
	return "{}"
}
