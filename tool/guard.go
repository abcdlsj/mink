package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/mink/config"
)

type Guard interface {
	Allow(ctx context.Context, cmd string) (bool, error)
}

var dangerousPrefixes = []string{
	"rm ", "rm\t",
	"mv ", "mv\t",
	"cp ", "cp\t",
	"git push", "git reset", "git checkout .",
	"docker run", "docker rm", "docker stop",
	"kubectl apply", "kubectl delete",
}

var sensitiveReadMarks = []string{
	"/.ssh/",
	"/.gnupg/",
	"/.aws/",
	"/.kube/",
	"/.docker/",
	"/.config/gcloud/",
	"/.config/gh/",
	"/.mink/",
	"/etc/ssh/",
	"/etc/ssl/private/",
}

var sensitiveReadNames = map[string]struct{}{
	".env":                                 {},
	".netrc":                               {},
	".npmrc":                               {},
	".pypirc":                              {},
	".git-credentials":                     {},
	"id_rsa":                               {},
	"id_ecdsa":                             {},
	"id_ed25519":                           {},
	"id_dsa":                               {},
	"authorized_keys":                      {},
	"known_hosts":                          {},
	"application_default_credentials.json": {},
}

var sensitiveReadTails = []string{
	"/etc/passwd",
	"/etc/shadow",
	"/etc/sudoers",
	"/etc/security/passwd",
}

func IsDangerous(raw string) bool {
	cmd := strings.TrimSpace(raw)
	for _, p := range dangerousPrefixes {
		if strings.HasPrefix(cmd, p) {
			return true
		}
	}
	return false
}

func (r *Registry) allow(ctx context.Context, name string, args json.RawMessage) (bool, error) {
	if r.guard == nil {
		return true, nil
	}

	raw, ok := guardedToolCall(name, args)
	if !ok {
		return true, nil
	}

	ok, err := r.guard.Allow(ctx, raw)
	if err != nil {
		return false, fmt.Errorf("tool guard: %w", err)
	}
	return ok, nil
}

func guardedToolCall(name string, args json.RawMessage) (string, bool) {
	switch name {
	case "bash":
		return guardedBash(args)
	case "read":
		return guardedRead(args)
	default:
		return "", false
	}
}

func guardedBash(args json.RawMessage) (string, bool) {
	var in struct {
		Cmd string `json:"cmd"`
	}
	if json.Unmarshal(args, &in) != nil {
		return "", false
	}
	cmd := strings.TrimSpace(in.Cmd)
	if cmd == "" || !IsDangerous(cmd) {
		return "", false
	}
	return cmd, true
}

func guardedRead(args json.RawMessage) (string, bool) {
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &in) != nil {
		return "", false
	}

	p := cleanReadPath(in.Path)
	if p == "" || !isSensitiveReadPath(p) {
		return "", false
	}
	return "read " + p, true
}

func cleanReadPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}

	if home, ok := expandHomePrefix(p); ok {
		p = home
	}

	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err == nil {
			p = abs
		}
	}

	p = filepath.Clean(p)
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		p = real
	}
	return p
}

func expandHomePrefix(p string) (string, bool) {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "~\\") {
		return "", false
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	if p == "~" {
		return home, true
	}

	tail := strings.TrimLeft(p[1:], "/\\")
	return filepath.Join(home, tail), true
}

func isSensitiveReadPath(p string) bool {
	norm := strings.ToLower(filepath.ToSlash(p))
	if norm == "" {
		return false
	}

	cfg := strings.ToLower(filepath.ToSlash(config.ConfigPath()))
	if norm == cfg {
		return true
	}

	name := strings.ToLower(filepath.Base(norm))
	if _, ok := sensitiveReadNames[name]; ok {
		return true
	}
	if strings.HasPrefix(name, ".env.") {
		return true
	}

	for _, tail := range sensitiveReadTails {
		if strings.HasSuffix(norm, tail) {
			return true
		}
	}
	for _, mark := range sensitiveReadMarks {
		if strings.Contains(norm, mark) {
			return true
		}
	}

	return false
}
