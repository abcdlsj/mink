package external

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func CommandVersion(ctx context.Context, command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, command, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return compactVersionLine(string(out))
}

func compactVersionLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len([]rune(s)) <= 80 {
		return s
	}
	return string([]rune(s)[:80])
}
