package agent

import (
	"encoding/json"
	"strings"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/tool"
)

type turnToolRecord struct {
	StateVersion int
	Output       string
	Error        string
}

func (a *Agent) resetTurnState() {
	a.turnToolHistory = make(map[string]turnToolRecord)
	a.turnStateVersion = 0
}

func (a *Agent) maybeBlockDuplicateToolCall(tc msg.ToolCall) error {
	fingerprint := toolFingerprint(tc)
	rec, ok := a.turnToolHistory[fingerprint]
	if !ok {
		return nil
	}
	if !isMutatingTool(tc.Name) && rec.StateVersion != a.turnStateVersion {
		return nil
	}

	details := rec.Output
	if rec.Error != "" {
		details = rec.Error
	}
	a.logWarn("tool_duplicate_blocked", map[string]any{
		"tool":          tc.Name,
		"args":          normalizeToolArgs(tc.Args),
		"state_version": a.turnStateVersion,
	})
	return tool.DuplicateError(tc.Name, details)
}

func (a *Agent) rememberToolCall(tc msg.ToolCall, output string, err error) {
	fingerprint := toolFingerprint(tc)
	rec := turnToolRecord{
		StateVersion: a.turnStateVersion,
		Output:       output,
	}
	if err != nil {
		rec.Error = err.Error()
	}
	a.turnToolHistory[fingerprint] = rec
	if isMutatingTool(tc.Name) {
		a.turnStateVersion++
	}
}

func toolFingerprint(tc msg.ToolCall) string {
	return tc.Name + ":" + normalizeToolArgs(tc.Args)
}

func normalizeToolArgs(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "null"
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return trimmed
	}
	if obj, ok := decoded.(map[string]any); ok {
		delete(obj, "_model")
		decoded = obj
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return trimmed
	}
	return string(out)
}

func isMutatingTool(name string) bool {
	switch name {
	case "read", "brave_search":
		return false
	default:
		return true
	}
}
