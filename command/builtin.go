package command

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type helpCmd struct {
	reg *Registry
}

func NewHelpCmd(reg *Registry) Command { return &helpCmd{reg: reg} }

func (c *helpCmd) Name() string { return "help" }
func (c *helpCmd) Desc() string { return "show available commands" }

func (c *helpCmd) Run(ctx context.Context, args []string) (string, error) {
	var b strings.Builder
	b.WriteString("Commands:\n")
	for _, cmd := range c.reg.All() {
		fmt.Fprintf(&b, "  !%s - %s\n", cmd.Name(), cmd.Desc())
	}
	b.WriteString("\nShell commands: !<cmd> (e.g., !ls -la)")
	return b.String(), nil
}

type toolsCmd struct {
	reg *tool.Registry
}

func NewToolsCmd(reg *tool.Registry) Command { return &toolsCmd{reg: reg} }

func (c *toolsCmd) Name() string { return "tools" }
func (c *toolsCmd) Desc() string { return "list available tools" }

func (c *toolsCmd) Run(ctx context.Context, args []string) (string, error) {
	var b strings.Builder
	b.WriteString("Tools:\n")
	for _, t := range c.reg.All() {
		fmt.Fprintf(&b, "  %s - %s\n", t.Name(), t.Desc())
	}
	return b.String(), nil
}

type sessionCmd struct {
	sm *session.Manager
}

func NewSessionCmd(sm *session.Manager) Command { return &sessionCmd{sm: sm} }

func (c *sessionCmd) Name() string { return "session" }
func (c *sessionCmd) Desc() string { return "session management (list/new)" }

func (c *sessionCmd) Run(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "usage: !session [list|new]", nil
	}

	switch args[0] {
	case "list":
		ids, err := c.sm.List()
		if err != nil {
			return "", err
		}
		if len(ids) == 0 {
			return "no sessions", nil
		}
		var b strings.Builder
		b.WriteString("Sessions:\n")
		for _, id := range ids {
			fmt.Fprintf(&b, "  %s\n", id)
		}
		return b.String(), nil
	case "new":
		s, err := c.sm.Create()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("created: %s", s.ID()), nil
	default:
		return "usage: !session [list|new]", nil
	}
}

type compactCmd struct {
	b *bus.Bus
}

func NewCompactCmd(b *bus.Bus) Command { return &compactCmd{b: b} }

func (c *compactCmd) Name() string { return "compact" }
func (c *compactCmd) Desc() string { return "compact current conversation context" }

func (c *compactCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}
	note := strings.TrimSpace(strings.Join(args, " "))

	if err := c.b.Pub(bus.Msg{
		Type:    bus.TypeSessionCompact,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: note,
	}); err != nil {
		return "", err
	}

	if note == "" {
		return "compact requested", nil
	}
	return "compact requested with note", nil
}

type tokensCmd struct {
	usage func(src string) (msg.TokenUsage, bool)
}

func NewTokensCmd(usage func(src string) (msg.TokenUsage, bool)) Command {
	return &tokensCmd{usage: usage}
}

func (c *tokensCmd) Name() string { return "tokens" }
func (c *tokensCmd) Desc() string { return "show estimated token usage for current session" }

func (c *tokensCmd) Run(ctx context.Context, args []string) (string, error) {
	src := bus.SourceFrom(ctx)
	if src == "" {
		src = bus.AddrPlatformCLI
	}

	u, ok := c.usage(src)
	if !ok {
		return "no active session for current source", nil
	}

	return fmt.Sprintf(
		"Estimated tokens\n  total: %d\n  messages: %d\n  input(user): %d\n  output(assistant): %d\n  system: %d\n  tool: %d\n  source: %s",
		u.Total,
		u.Messages,
		u.Input,
		u.Output,
		u.System,
		u.Tool,
		u.Source,
	), nil
}

type ModelInfo struct {
	Models map[string]config.ModelConfig
	Active string
}

type modelsCmd struct {
	info func() ModelInfo
}

func NewModelsCmd(info func() ModelInfo) Command { return &modelsCmd{info: info} }

func (c *modelsCmd) Name() string { return "models" }
func (c *modelsCmd) Desc() string { return "list available models" }

func (c *modelsCmd) Run(ctx context.Context, args []string) (string, error) {
	info := c.info()
	if len(info.Models) == 0 {
		return "no models configured", nil
	}

	names := make([]string, 0, len(info.Models))
	for k := range info.Models {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("Models:\n")
	for _, name := range names {
		mc := info.Models[name]
		marker := "  "
		if name == info.Active {
			marker = "* "
		}
		fmt.Fprintf(&b, "  %s%s (%s/%s)\n", marker, name, mc.Provider, mc.Model)
	}
	return b.String(), nil
}

type modelCmd struct {
	switchFn func(name string) error
}

func NewModelCmd(switchFn func(name string) error) Command { return &modelCmd{switchFn: switchFn} }

func (c *modelCmd) Name() string { return "model" }
func (c *modelCmd) Desc() string { return "switch model (!model <name>)" }

func (c *modelCmd) Run(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "usage: !model <name>", nil
	}
	name := args[0]
	if err := c.switchFn(name); err != nil {
		return "", err
	}
	return fmt.Sprintf("switched to %s", name), nil
}
