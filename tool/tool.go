package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Tool struct {
	Name   string
	Desc   string
	Schema map[string]any
	Run    func(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	tools map[string]*Tool
}

func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]*Tool)}
	r.addCore()
	return r
}

func (r *Registry) addCore() {
	r.tools["read"] = &Tool{
		Name: "read",
		Desc: "Read file contents",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]string{"type": "string"}},
			"required":   []string{"path"},
		},
		Run: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Path string `json:"path"` }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			b, err := os.ReadFile(p.Path)
			return string(b), err
		},
	}

	r.tools["write"] = &Tool{
		Name: "write",
		Desc: "Write file contents",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]string{"type": "string"}, "content": map[string]string{"type": "string"}},
			"required":   []string{"path", "content"},
		},
		Run: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Path, Content string }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			os.MkdirAll(filepath.Dir(p.Path), 0755)
			if err := os.WriteFile(p.Path, []byte(p.Content), 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes", len(p.Content)), nil
		},
	}

	r.tools["edit"] = &Tool{
		Name: "edit",
		Desc: "Edit file by search/replace",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]string{"type": "string"}, "old": map[string]string{"type": "string"}, "new": map[string]string{"type": "string"}},
			"required":   []string{"path", "old", "new"},
		},
		Run: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Path, Old, New string }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			b, err := os.ReadFile(p.Path)
			if err != nil {
				return "", err
			}
			s := string(b)
			if !strings.Contains(s, p.Old) {
				return "", fmt.Errorf("not found")
			}
			s = strings.Replace(s, p.Old, p.New, 1)
			return "", os.WriteFile(p.Path, []byte(s), 0644)
		},
	}

	r.tools["bash"] = &Tool{
		Name: "bash",
		Desc: "Execute bash command",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"cmd": map[string]string{"type": "string"}, "cwd": map[string]string{"type": "string"}},
			"required":   []string{"cmd"},
		},
		Run: func(ctx context.Context, a json.RawMessage) (string, error) {
			var p struct{ Cmd, Cwd string }
			if err := json.Unmarshal(a, &p); err != nil {
				return "", err
			}
			c := exec.CommandContext(ctx, "bash", "-c", p.Cmd)
			if p.Cwd != "" {
				c.Dir = p.Cwd
			}
			out, err := c.CombinedOutput()
			return string(out), err
		},
	}
}

func (r *Registry) Get(name string) *Tool {
	return r.tools[name]
}

func (r *Registry) All() []*Tool {
	var s []*Tool
	for _, t := range r.tools {
		s = append(s, t)
	}
	return s
}
