package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/tool"
)

type Audit func(action, name string)

func RegisterTools(reg *tool.Registry, loader *Loader, audit ...Audit) {
	var fn Audit
	if len(audit) > 0 {
		fn = audit[0]
	}
	reg.Register(&listTool{loader: loader, audit: fn})
	reg.Register(&describeTool{loader: loader, audit: fn})
}

type listTool struct {
	loader *Loader
	audit  Audit
}

func (t *listTool) Name() string { return "skills_list" }
func (t *listTool) Desc() string { return "List available skills" }
func (t *listTool) Schema() map[string]any {
	return tool.ObjectSchema()
}
func (t *listTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	skills := t.loader.Discover()
	if len(skills) == 0 {
		return "(no skills)", nil
	}

	var b strings.Builder
	for _, s := range skills {
		if t.audit != nil {
			t.audit("listed", s.Name)
		}
		fmt.Fprintln(&b, s.Card())
	}
	return b.String(), nil
}

type describeTool struct {
	loader *Loader
	audit  Audit
}

func (t *describeTool) Name() string { return "skills_describe" }
func (t *describeTool) Desc() string { return "Load full skill body by name" }
func (t *describeTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("name", "string", "Skill name"),
		tool.Required("name"),
	)
}
func (t *describeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", tool.ParseError("skills_describe", err.Error(), string(args))
	}

	if params.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	skill := t.loader.Load(params.Name)
	if skill == nil {
		return "", fmt.Errorf("skill not found: %s", params.Name)
	}
	if t.audit != nil {
		t.audit("described", skill.Name)
		t.audit("used", skill.Name)
	}

	return skill.Body, nil
}
