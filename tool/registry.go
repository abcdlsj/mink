package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/abcdlsj/sumi/llm"
)

type Tool interface {
	Name() string
	Desc() string
	Schema() map[string]any
	Run(context.Context, json.RawMessage) (string, error)
}

type RiskCategory string

const (
	RiskSafe         RiskCategory = "safe"
	RiskShell        RiskCategory = "shell"
	RiskNetwork      RiskCategory = "network"
	RiskNotification RiskCategory = "notification"
)

type RiskDescriber interface {
	Risk() RiskCategory
}

type Call struct {
	Tool   string
	Action string
	Args   json.RawMessage
}

type Guard interface {
	Allow(context.Context, Call) (bool, error)
}

type Registry struct {
	workspace string
	tools     map[string]Tool
	guard     Guard
}

func NewRegistry(workspace string) *Registry {
	r := &Registry{
		workspace: workspace,
		tools:     map[string]Tool{},
	}
	r.registerBuiltins()
	return r
}

func (r *Registry) registerBuiltins() {
	r.Register(&Read{workspace: r.workspace})
	r.Register(&Write{workspace: r.workspace})
	r.Register(&Edit{workspace: r.workspace})
	r.Register(&Bash{workspace: r.workspace})
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) SetGuard(g Guard) {
	r.guard = g
}

func (r *Registry) Guard() Guard {
	if r == nil {
		return nil
	}
	return r.guard
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
	if err := enforceRunContextPermission(ctx, t, args); err != nil {
		return "", err
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

func RiskOf(t Tool) RiskCategory {
	if t == nil {
		return RiskSafe
	}
	if d, ok := t.(RiskDescriber); ok {
		return d.Risk()
	}
	return RiskSafe
}

func (r *Registry) allow(ctx context.Context, name string, args json.RawMessage) (bool, error) {
	if r == nil || r.guard == nil {
		return true, nil
	}
	call, ok := guardedCall(r.workspace, name, args)
	if !ok {
		return true, nil
	}
	return r.guard.Allow(ctx, call)
}

func ObjectSchema(parts ...map[string]any) map[string]any {
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

func Prop(name, typ, desc string) map[string]any {
	return map[string]any{name: map[string]any{"type": typ, "description": desc}}
}

func StringArrayProp(name, desc string) map[string]any {
	return map[string]any{name: map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}}
}

func Required(names ...string) map[string]any {
	req := make([]string, len(names))
	copy(req, names)
	return map[string]any{"_required": req}
}

func objectSchema(parts ...map[string]any) map[string]any { return ObjectSchema(parts...) }
func prop(name, typ, desc string) map[string]any          { return Prop(name, typ, desc) }
func required(names ...string) map[string]any             { return Required(names...) }
