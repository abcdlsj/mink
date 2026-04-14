package external

import (
	"strings"

	agrt "github.com/abcdlsj/mink/agent/runtime"
)

func Driver(name string) (agrt.Driver, bool) {
	switch strings.TrimSpace(name) {
	case "claude":
		return ClaudeDriver(), true
	case "codex":
		return CodexDriver(), true
	default:
		return agrt.Driver{}, false
	}
}
