package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/tool"
)

func RegisterTools(reg *tool.Registry, loader *Loader) {
	reg.Register(&listTool{loader: loader})
	reg.Register(&describeTool{loader: loader})
}

type listTool struct {
	loader *Loader
}

func (t *listTool) Name() string { return "skills_list" }
func (t *listTool) Desc() string { return "List available skills" }
func (t *listTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *listTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	skills := t.loader.Discover()
	if len(skills) == 0 {
		return "(no skills)", nil
	}

	var b strings.Builder
	for _, s := range skills {
		fmt.Fprintf(&b, "%s: %s\n", s.Name, s.Desc)
	}
	return b.String(), nil
}

type describeTool struct {
	loader *Loader
}

func (t *describeTool) Name() string { return "skills_describe" }
func (t *describeTool) Desc() string { return "Load full skill body by name" }
func (t *describeTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name",
			},
		},
		"required": []string{"name"},
	}
}
func (t *describeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", tool.ParseErrorWithInput("skills_describe", err.Error(), string(args))
	}

	if params.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	skill := t.loader.Load(params.Name)
	if skill == nil {
		return "", fmt.Errorf("skill not found: %s", params.Name)
	}

	return skill.Body, nil
}
