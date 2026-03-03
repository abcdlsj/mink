# Mink Smart Model Selection

## Goal

LLM determines task complexity and automatically routes simple tasks to cheaper models.

## Config

```toml
# config.toml
default = "claude"
cheap = "kimi"
```

## Implementation

### 1. Sel

```go
// llm/sel.go
package llm

type Sel struct{ p, c Provider }

func NewSel(p, c Provider) *Sel { return &Sel{p, c} }

func (s *Sel) P(m string) Provider {
	if m == "cheap" {
		return s.c
	}
	return s.p
}
```

### 2. Registry

```go
// tool/core.go
var modelable = map[string]bool{
	"bash":  true,
	"read":  true,
	"write": true,
	"edit":  true,
}

func (r *Registry) Register(t Tool) {
	schema := t.Schema()
	if modelable[t.Name()] {
		schema["properties"].(map[string]any)["_model"] = map[string]any{
			"type":        "string",
			"enum":        []string{"default", "cheap"},
			"description": "Optional, system uses this to select model",
		}
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) (string, error) {
	m := "default"
	if args != nil {
		var p map[string]json.RawMessage
		if json.Unmarshal(args, &p); p != nil {
			if v, ok := p["_model"]; ok {
				m = strings.Trim(string(v), `"`)
				delete(p, "_model")
				args, _ = json.Marshal(p)
			}
		}
	}

	provider := r.sel.P(m)
	// ...
}
```

### 3. App

```go
// app.go
func (a *App) init() error {
	models := make(map[string]llm.Provider)
	for name, cfg := range a.cfg.Models {
		models[name] = llm.NewProvider(llm.Config{...})
	}

	a.sel = llm.NewSel(models[a.cfg.Default], models[a.cfg.Cheap])

	reg := tool.NewRegistry(a.sel)
	reg.Register(&Bash{})
	reg.Register(&Read{})
	reg.Register(&Write{})
	reg.Register(&Edit{})
	reg.Register(&Spawn{})
	// ...
}
```

## Notes

- Per-call basis, each tool call independently decides which model to use
- Non-invasive, Tool authors are unaware of this feature
