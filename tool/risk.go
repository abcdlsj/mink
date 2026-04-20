package tool

import (
	"os"
	"path/filepath"
	"strings"
)

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

func isSensitiveReadPath(p string) bool {
	norm := strings.ToLower(filepath.ToSlash(p))
	if norm == "" {
		return false
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

func shouldGuardWritePath(workspace, p string) bool {
	if isSensitiveReadPath(p) {
		return true
	}
	if isSystemWritePath(p) {
		return true
	}
	return isOutsideWorkspace(workspace, p)
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

func isOutsideWorkspace(workspace, p string) bool {
	root := resolvePath(workspace, workspace)
	if root == "" {
		wd, err := os.Getwd()
		if err != nil || wd == "" {
			return false
		}
		root = resolvePath("", wd)
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
