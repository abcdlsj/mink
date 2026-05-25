package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

const maxAttachedFile = 256 * 1024

func (a *App) runProjectCommand(ctx context.Context, args []string) (string, error) {
	action := "view"
	if len(args) > 0 {
		action = strings.TrimSpace(args[0])
	}
	path := projectContextPath(a.cfg.Workspace)
	switch action {
	case "path":
		return path, nil
	case "edit":
		if err := ensureProjectContext(path, a.cfg.Workspace); err != nil {
			return "", err
		}
		return "project context: " + path, nil
	case "init":
		if fileExists(path) {
			return "project context already exists: " + path, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(defaultProjectContext(a.cfg.Workspace)), 0o644); err != nil {
			return "", err
		}
		return "created project context: " + path, nil
	case "view":
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return "no project context. run /project init", nil
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	default:
		return "usage: /project [view|init|edit|path]", nil
	}
}

func (a *App) runFileCommand(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "usage: /file <path>", nil
	}
	path, err := resolveWorkspacePath(a.cfg.Workspace, strings.Join(args, " "))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("file is a directory: %s", path)
	}
	if info.Size() > maxAttachedFile {
		return "", fmt.Errorf("file too large: %s (%d bytes, max %d)", path, info.Size(), maxAttachedFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || hasNUL(data) {
		return "", fmt.Errorf("only UTF-8 text files are supported for now: %s", path)
	}
	rel, _ := filepath.Rel(a.cfg.Workspace, path)
	content := fmt.Sprintf("[Attached file: %s]\n\n```text\n%s\n```", rel, strings.TrimRight(string(data), "\n"))
	s, err := a.sessions.Current(command.SourceFrom(ctx))
	if err != nil {
		return "", err
	}
	s.Add(msg.Message{Role: "system", Content: content, Timestamp: time.Now()})
	if err := a.sessions.Save(s); err != nil {
		return "", err
	}
	return fmt.Sprintf("attached %s (%d bytes)", rel, len(data)), nil
}

func (a *App) runUsageCommand(ctx context.Context, args []string) (string, error) {
	cur, err := a.sessions.Current(command.SourceFrom(ctx))
	if err != nil {
		return "", err
	}
	sessions, err := a.sessions.List()
	if err != nil {
		return "", err
	}
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var current, month, all usageTotal
	current.AddUsage(cur.Usage, time.Time{})
	for _, s := range sessions {
		all.AddUsage(s.Usage, time.Time{})
		month.AddUsage(s.Usage, monthStart)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Usage (%s)\n", a.currentModel())
	fmt.Fprintf(&b, "\nCurrent session:\n%s\n", current.String())
	fmt.Fprintf(&b, "\nThis month:\n%s\n", month.String())
	fmt.Fprintf(&b, "\nAll time:\n%s", all.String())
	if all.Total == 0 {
		b.WriteString("\n\nNo provider token usage recorded yet. Sessions created before usage metadata will show zero.")
	}
	return b.String(), nil
}

type usageTotal struct {
	Input  int
	Output int
	Total  int
	Calls  int
}

func (u *usageTotal) AddUsage(v session.Usage, since time.Time) {
	if since.IsZero() {
		u.Input += v.Input
		u.Output += v.Output
		u.Total += v.Total
		u.Calls += v.Calls
		return
	}
	for _, r := range v.Records {
		if r.At.Before(since) {
			continue
		}
		u.Input += r.Input
		u.Output += r.Output
		u.Total += r.Total
		u.Calls++
	}
}

func (u usageTotal) String() string {
	return fmt.Sprintf("  calls: %d\n  input tokens: %d\n  output tokens: %d\n  total tokens: %d", u.Calls, u.Input, u.Output, u.Total)
}

func projectContextPath(workspace string) string {
	return filepath.Join(strings.TrimSpace(workspace), ".sumi", "context.md")
}

func loadProjectContext(workspace string) string {
	data, err := os.ReadFile(projectContextPath(workspace))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func ensureProjectContext(path, workspace string) error {
	if fileExists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultProjectContext(workspace)), 0o644)
}

func defaultProjectContext(workspace string) string {
	name := filepath.Base(strings.TrimRight(workspace, string(filepath.Separator)))
	lines := []string{
		"# Project Context",
		"",
		"Project: " + name,
	}
	if detected := detectProject(workspace); len(detected) > 0 {
		sort.Strings(detected)
		lines = append(lines, "", "Detected:", strings.Join(prefixLines(detected, "- "), "\n"))
	}
	lines = append(lines, "", "Notes:", "- Keep this file short and factual.")
	return strings.Join(lines, "\n") + "\n"
}

func detectProject(workspace string) []string {
	checks := map[string]string{
		"go.mod":             "Go module",
		"package.json":       "Node/JavaScript project",
		"pyproject.toml":     "Python project",
		"Cargo.toml":         "Rust project",
		"Makefile":           "Makefile",
		"docker-compose.yml": "Docker Compose",
	}
	var out []string
	for file, label := range checks {
		if fileExists(filepath.Join(workspace, file)) {
			out = append(out, label)
		}
	}
	return out
}

func prefixLines(lines []string, prefix string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = prefix + line
	}
	return out
}

func resolveWorkspacePath(workspace, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file is outside workspace: %s", abs)
	}
	return abs, nil
}

func hasNUL(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
