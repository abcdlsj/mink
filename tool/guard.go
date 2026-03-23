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

type Approval int

const (
	Denied Approval = iota
	AllowOnce
	AllowAlways
)

type InteractiveGuard interface {
	Approve(ctx context.Context, cmd string) (Approval, error)
}

var shellWrappers = map[string]struct{}{
	"sudo":    {},
	"env":     {},
	"command": {},
	"nohup":   {},
	"time":    {},
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
	cmd := strings.TrimSpace(strings.ToLower(raw))
	if cmd == "" {
		return false
	}
	for _, seg := range splitShellSegments(cmd) {
		if isDangerousSegment(seg) {
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
	case "write":
		return guardedWrite(args)
	case "edit":
		return guardedEdit(args)
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

	p := resolvePath(in.Path)
	if p == "" || !isSensitiveReadPath(p) {
		return "", false
	}
	return "read " + p, true
}

func guardedWrite(args json.RawMessage) (string, bool) {
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &in) != nil {
		return "", false
	}

	p := resolvePath(in.Path)
	if p == "" || !shouldGuardWritePath(p) {
		return "", false
	}
	return "write " + p, true
}

func guardedEdit(args json.RawMessage) (string, bool) {
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &in) != nil {
		return "", false
	}

	p := resolvePath(in.Path)
	if p == "" || !shouldGuardWritePath(p) {
		return "", false
	}
	return "edit " + p, true
}

func resolvePath(raw string) string {
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

func shouldGuardWritePath(p string) bool {
	if isSensitiveReadPath(p) {
		return true
	}
	if isSystemWritePath(p) {
		return true
	}
	return isOutsideWorkspace(p)
}

func isSystemWritePath(p string) bool {
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
	for _, prefix := range []string{
		"/etc/",
		"/usr/",
		"/bin/",
		"/sbin/",
		"/var/",
		"/opt/",
		"/library/",
		"/system/",
	} {
		if strings.HasPrefix(norm, prefix) {
			return true
		}
	}
	return false
}

func isOutsideWorkspace(p string) bool {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return false
	}

	root := resolvePath(wd)
	if root == "" {
		root = filepath.Clean(wd)
	}
	target := filepath.Clean(p)

	root = filepath.Clean(root)
	if target == root {
		return false
	}
	sep := string(os.PathSeparator)
	return !strings.HasPrefix(target, root+sep)
}

func splitShellSegments(cmd string) []string {
	replacer := strings.NewReplacer(
		"&&", ";",
		"||", ";",
		"|", ";",
		"\n", ";",
	)
	parts := strings.Split(replacer.Replace(cmd), ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		seg := strings.TrimSpace(part)
		if seg == "" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

func isDangerousSegment(seg string) bool {
	fields := strings.Fields(seg)
	if len(fields) == 0 {
		return false
	}

	i := 0
	for i < len(fields) {
		f := fields[i]
		if _, ok := shellWrappers[f]; ok {
			i++
			continue
		}
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "=") && !strings.HasSuffix(f, "=") {
			i++
			continue
		}
		break
	}
	if i >= len(fields) {
		return false
	}

	cmd := fields[i]
	rest := fields[i+1:]
	switch cmd {
	case "rm", "mv", "cp", "dd", "mkfs", "chmod", "chown":
		return true
	case "shutdown", "reboot":
		return true
	case "git":
		if len(rest) == 0 {
			return false
		}
		sub := rest[0]
		if sub == "push" || sub == "reset" {
			return true
		}
		if sub == "checkout" {
			for _, a := range rest[1:] {
				if a == "." || a == "--" {
					return true
				}
			}
		}
	case "docker":
		if len(rest) == 0 {
			return false
		}
		sub := rest[0]
		return sub == "run" || sub == "rm" || sub == "stop"
	case "kubectl":
		if len(rest) == 0 {
			return false
		}
		sub := rest[0]
		return sub == "apply" || sub == "delete"
	}
	return false
}
