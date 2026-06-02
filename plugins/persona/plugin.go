package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/command"
	corepersona "github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/tool"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterTool(listTool{a: a})
		a.RegisterTool(createTool{a: a})
		a.RegisterTool(invokeTool{a: a})
		return nil
	}
}

type listTool struct{ a *app.App }

func (t listTool) Name() string           { return "list_personas" }
func (t listTool) Desc() string           { return "List available personas and their runtimes" }
func (t listTool) Schema() map[string]any { return tool.ObjectSchema() }

func (t listTool) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	ps := t.a.Personas().List()
	if len(ps) == 0 {
		return "no personas defined", nil
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	var b strings.Builder
	for _, p := range ps {
		rt := p.Runtime
		if rt == "" {
			rt = "(default)"
		}
		fmt.Fprintf(&b, "- %s [%s] %s\n", p.ID, rt, p.Description)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

type createArgs struct {
	ID          string   `json:"id"`
	Display     string   `json:"display"`
	Runtime     string   `json:"runtime"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
	Soul        string   `json:"soul"`
}

type createTool struct{ a *app.App }

func (t createTool) Name() string { return "create_persona" }
func (t createTool) Desc() string {
	return "Create a new persona with its own SOUL and memory directory"
}
func (t createTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("id", "string", "Persona id (lowercase, stable)"),
		tool.Prop("display", "string", "Display name"),
		tool.Prop("runtime", "string", "Runtime name (claude/codex/native/...)"),
		tool.Prop("description", "string", "Role description"),
		tool.StringArrayProp("tools", "Optional tool allowlist"),
		tool.Prop("soul", "string", "SOUL.md content (persona prompt)"),
		tool.Required("id"),
	)
}

func (t createTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in createArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("create_persona: %w", err)
	}
	if strings.TrimSpace(in.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	p, err := t.a.Personas().Create(in.ID, corepersona.Meta{
		Display:     in.Display,
		Runtime:     in.Runtime,
		Description: in.Description,
		Tools:       in.Tools,
	}, in.Soul)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("created persona %s at %s", p.ID, p.Root), nil
}

type invokeArgs struct {
	ID    string `json:"id"`
	Input string `json:"input"`
}

type invokeTool struct{ a *app.App }

func (t invokeTool) Name() string { return "invoke_persona" }
func (t invokeTool) Desc() string {
	return "Run a single turn as the given persona in the current channel"
}
func (t invokeTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("id", "string", "Persona id"),
		tool.Prop("input", "string", "Message to send to the persona"),
		tool.Required("id", "input"),
	)
}

func (t invokeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in invokeArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invoke_persona: %w", err)
	}
	if strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Input) == "" {
		return "", fmt.Errorf("id and input are required")
	}
	src := command.SourceFrom(ctx)
	return t.a.HandleInputAs(ctx, src, in.ID, in.Input)
}
