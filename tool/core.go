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
)

// Tool 工具接口
type Tool interface {
	Name() string
	Desc() string
	Schema() map[string]any
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry 工具注册表
type Registry struct {
	tools map[string]Tool
	guard Guard
}

func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	// 注册核心工具
	r.Register(&Read{})
	r.Register(&Write{})
	r.Register(&Edit{})
	r.Register(&Bash{})
	return r
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) SetGuard(g Guard) {
	r.guard = g
}

func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t := r.Get(name)
	if t == nil {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	ok, err := r.allow(ctx, name, args)
	if err != nil {
		return "", err
	}
	if !ok {
		return "cancelled", nil
	}

	return t.Run(ctx, args)
}

func (r *Registry) All() []Tool {
	keys := make([]string, 0, len(r.tools))
	for k := range r.tools {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	list := make([]Tool, 0, len(keys))
	for _, k := range keys {
		list = append(list, r.tools[k])
	}
	return list
}

// Read 读取文件
type Read struct{}

func (r *Read) Name() string { return "read" }
func (r *Read) Desc() string { return "Read file contents" }
func (r *Read) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]string{"type": "string", "description": "File path"},
		},
		"required": []string{"path"},
	}
}
func (r *Read) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Write 写入文件
type Write struct{}

func (w *Write) Name() string { return "write" }
func (w *Write) Desc() string { return "Write file contents (overwrite)" }
func (w *Write) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]string{"type": "string", "description": "File path"},
			"content": map[string]string{"type": "string", "description": "Content to write"},
		},
		"required": []string{"path", "content"},
	}
}
func (w *Write) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	dir := filepath.Dir(params.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(params.Content), params.Path), nil
}

// Edit 编辑文件
type Edit struct{}

func (e *Edit) Name() string { return "edit" }
func (e *Edit) Desc() string { return "Edit file by search and replace" }
func (e *Edit) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]string{"type": "string", "description": "File path"},
			"old":  map[string]string{"type": "string", "description": "Text to search"},
			"new":  map[string]string{"type": "string", "description": "Replacement text"},
		},
		"required": []string{"path", "old", "new"},
	}
}
func (e *Edit) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	content := string(data)
	if !strings.Contains(content, params.Old) {
		return "", fmt.Errorf("search text not found in file")
	}
	newContent := strings.Replace(content, params.Old, params.New, 1)
	if err := os.WriteFile(params.Path, []byte(newContent), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Edited %s", params.Path), nil
}

// Bash 执行命令
type Bash struct{}

func (b *Bash) Name() string { return "bash" }
func (b *Bash) Desc() string { return "Execute bash command" }
func (b *Bash) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cmd": map[string]string{"type": "string", "description": "Command to execute"},
			"cwd": map[string]string{"type": "string", "description": "Working directory (optional)"},
		},
		"required": []string{"cmd"},
	}
}
func (b *Bash) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Cmd string `json:"cmd"`
		Cwd string `json:"cwd,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", ParseError("bash", err.Error())
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", params.Cmd)
	if params.Cwd != "" {
		cmd.Dir = params.Cwd
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return string(output), WrapError("bash", ctx.Err())
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(output), ExecError("bash", params.Cmd, exitErr.ExitCode(), string(output))
		}
		return string(output), WrapError("bash", err)
	}
	return string(output), nil
}
