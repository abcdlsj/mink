package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	commandbuiltin "github.com/abcdlsj/mink/command/builtin"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/platform/cliapp"
)

func trimSource(src string) string {
	return strings.TrimSpace(src)
}

func (a *App) cliStatus() func() cliapp.StatusInfo {
	home, _ := os.UserHomeDir()
	pwd, _ := os.Getwd()
	ws := pwd
	if home != "" && strings.HasPrefix(ws, home) {
		ws = "~" + ws[len(home):]
	}
	cliSource := a.cliSource()

	return func() cliapp.StatusInfo {
		model := a.disp.ModelDisplayName()
		u, _ := a.disp.Usage(cliSource)
		sessID, _ := a.sm.CurrentID(cliSource)
		return cliapp.StatusInfo{
			Model:     model,
			TokenIn:   u.Input,
			TokenOut:  u.Output,
			Workspace: ws,
			Session:   sessID,
			Agents:    a.cliAgents(),
			Team:      a.teamStatusForSource(context.Background(), cliSource),
		}
	}
}

func (a *App) cliAgents() []cliapp.AgentInfo {
	if a.reg == nil {
		return nil
	}
	states := a.reg.All()
	sort.Slice(states, func(i, j int) bool {
		if states[i].Status != states[j].Status {
			return states[i].Status < states[j].Status
		}
		left := states[i].Descriptor.Name
		if left == "" {
			left = states[i].Descriptor.ID
		}
		right := states[j].Descriptor.Name
		if right == "" {
			right = states[j].Descriptor.ID
		}
		return left < right
	})
	agents := make([]cliapp.AgentInfo, 0, len(states))
	for _, state := range states {
		agents = append(agents, cliapp.AgentInfo{
			ID:     state.Descriptor.ID,
			Name:   state.Descriptor.Name,
			Status: string(state.Status),
			Runs:   len(state.ActiveRuns),
			Caps:   state.Descriptor.Capabilities,
		})
	}
	return agents
}

func (a *App) cliSessionMessages(source string) func() []msg.Message {
	return func() []msg.Message {
		if source == "" {
			return nil
		}
		sess, err := a.sm.Current(source)
		if err != nil || sess == nil {
			return nil
		}
		return sess.View().Messages
	}
}

func (a *App) modelsInfo() commandbuiltin.ModelInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return commandbuiltin.ModelInfo{Models: a.cfg.Models, Active: a.cfg.ActiveModel}
}

func (a *App) agentsInfo() string {
	if a.reg == nil {
		return "no agents configured"
	}
	states := a.reg.All()
	if len(states) == 0 {
		return "no agents registered"
	}
	var b strings.Builder
	b.WriteString("Agents:\n")
	for _, s := range states {
		fmt.Fprintf(&b, "  %s (%s) [%s] caps=%v runs=%d\n",
			s.Descriptor.ID, s.Descriptor.Name, s.Status,
			s.Descriptor.Capabilities, len(s.ActiveRuns))
	}
	return b.String()
}

func (a *App) switchModel(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !config.ResolveModel(&a.cfg, name) {
		return fmt.Errorf("model %q not found", name)
	}

	p, err := newProviderFromModel(a.cfg.Active)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	sel := newSelector(a.cfg, p)

	a.p = p
	a.disp.SetLLM(p, sel)
	a.disp.SetConfig(a.cfg)
	a.disp.ResetAgents()

	return config.SaveActiveModel(name)
}
