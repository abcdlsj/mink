package builtin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
)

type ModelInfo struct {
	Models map[string]config.ModelConfig
	Active string
}

type modelsCmd struct {
	info func() ModelInfo
}

func NewModelsCmd(info func() ModelInfo) command.Command { return &modelsCmd{info: info} }

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
		fmt.Fprintf(&b, "  %s%s (%s/%s", marker, name, mc.Provider, mc.Model)
		if mc.ContextWindow > 0 {
			fmt.Fprintf(&b, " ctx=%d", mc.ContextWindow)
		}
		if mc.MaxTokens > 0 {
			fmt.Fprintf(&b, " max=%d", mc.MaxTokens)
		}
		b.WriteString(")\n")
	}
	return b.String(), nil
}

type modelCmd struct {
	switchFn func(name string) error
}

func NewModelCmd(switchFn func(name string) error) command.Command {
	return &modelCmd{switchFn: switchFn}
}

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

type agentsCmd struct {
	info func() string
}

func NewAgentsCmd(info func() string) command.Command { return &agentsCmd{info: info} }

func (c *agentsCmd) Name() string { return "agents" }
func (c *agentsCmd) Desc() string { return "show registered agents" }

func (c *agentsCmd) Run(ctx context.Context, _ []string) (string, error) {
	return c.info(), nil
}
