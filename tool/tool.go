package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/llm"
)

type Tool interface {
	Name() string
	Desc() string
	Schema() map[string]any
	Run(context.Context, json.RawMessage) (string, error)
}

type Registry struct {
	workspace string
	tools     map[string]Tool
}

func NewRegistry(workspace string) *Registry {
	r := &Registry{
		workspace: workspace,
		tools:     map[string]Tool{},
	}
	r.Register(&Read{workspace: workspace})
	r.Register(&Write{workspace: workspace})
	r.Register(&Edit{workspace: workspace})
	r.Register(&Bash{workspace: workspace})
	return r
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

func (r *Registry) All() []Tool {
	keys := make([]string, 0, len(r.tools))
	for k := range r.tools {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Tool, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.tools[k])
	}
	return out
}

func (r *Registry) Definitions() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.All() {
		out = append(out, llm.Tool{
			Type: "function",
			Function: &llm.FunctionDef{
				Name:        t.Name(),
				Description: t.Desc(),
				Parameters:  t.Schema(),
			},
		})
	}
	return out
}

func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t := r.Get(name)
	if t == nil {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Run(ctx, args)
}

type Read struct {
	workspace string
}

func (t *Read) Name() string { return "read" }
func (t *Read) Desc() string { return "Read file contents" }
func (t *Read) Schema() map[string]any {
	return objectSchema(
		prop("path", "string", "File path to read"),
		required("path"),
	)
}
func (t *Read) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", ParseError("read", err.Error(), string(args))
	}
	data, err := os.ReadFile(resolvePath(t.workspace, in.Path))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type Write struct {
	workspace string
}

func (t *Write) Name() string { return "write" }
func (t *Write) Desc() string { return "Write file contents" }
func (t *Write) Schema() map[string]any {
	return objectSchema(
		prop("path", "string", "File path to write"),
		prop("content", "string", "File content"),
		required("path", "content"),
	)
}
func (t *Write) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", ParseError("write", err.Error(), string(args))
	}
	path := resolvePath(t.workspace, in.Path)
	if err := ensureWritePath(t.workspace, path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(in.Content), path), nil
}

type Edit struct {
	workspace string
}

func (t *Edit) Name() string { return "edit" }
func (t *Edit) Desc() string { return "Edit file by search and replace" }
func (t *Edit) Schema() map[string]any {
	return objectSchema(
		prop("path", "string", "File path"),
		prop("old", "string", "Text to replace"),
		prop("new", "string", "Replacement text"),
		required("path", "old", "new"),
	)
}
func (t *Edit) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", ParseError("edit", err.Error(), string(args))
	}
	path := resolvePath(t.workspace, in.Path)
	if err := ensureWritePath(t.workspace, path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	if !strings.Contains(content, in.Old) {
		return "", fmt.Errorf("search text not found")
	}
	content = strings.Replace(content, in.Old, in.New, 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

type Bash struct {
	workspace string
}

func (t *Bash) Name() string { return "bash" }
func (t *Bash) Desc() string { return "Run a shell command in the workspace" }
func (t *Bash) Schema() map[string]any {
	return objectSchema(
		prop("cmd", "string", "Shell command to run"),
		required("cmd"),
	)
}
func (t *Bash) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", ParseError("bash", err.Error(), string(args))
	}
	if err := guardCommand(in.Cmd); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", in.Cmd)
	if t.workspace != "" {
		cmd.Dir = t.workspace
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(out), ExecError("bash", in.Cmd, exitErr.ExitCode(), string(out))
		}
		return string(out), err
	}
	return string(out), nil
}

func resolvePath(workspace, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return workspace
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if workspace == "" {
		abs, err := filepath.Abs(path)
		if err == nil {
			return abs
		}
		return filepath.Clean(path)
	}
	return filepath.Join(workspace, path)
}

func ensureWritePath(workspace, path string) error {
	if workspace == "" {
		return nil
	}
	rel, err := filepath.Rel(workspace, path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("write outside workspace is not allowed: %s", path)
	}
	return nil
}

func guardCommand(cmd string) error {
	raw := strings.ToLower(strings.TrimSpace(cmd))
	bad := []string{
		"sudo ",
		"rm -rf /",
		"rm -rf ~",
		"git reset --hard",
		"mkfs",
		"shutdown",
		"reboot",
		"dd if=",
	}
	for _, token := range bad {
		if strings.Contains(raw, token) {
			return fmt.Errorf("command blocked: %s", token)
		}
	}
	return nil
}

func objectSchema(parts ...map[string]any) map[string]any {
	out := map[string]any{"type": "object", "properties": map[string]any{}}
	props := out["properties"].(map[string]any)
	for _, part := range parts {
		if part == nil {
			continue
		}
		if req, ok := part["_required"]; ok {
			out["required"] = req
			continue
		}
		for k, v := range part {
			props[k] = v
		}
	}
	return out
}

func prop(name, typ, desc string) map[string]any {
	return map[string]any{name: map[string]any{"type": typ, "description": desc}}
}

func required(names ...string) map[string]any {
	req := make([]string, len(names))
	copy(req, names)
	return map[string]any{"_required": req}
}
