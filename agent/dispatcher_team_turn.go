package agent

import (
	"context"
	"strings"

	agrt "github.com/abcdlsj/mink/agent/runtime"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
)

func (d *Dispatcher) prepareTeamTurn(ctx context.Context, src string, rt *NativeRuntime) (TeamTurn, func(), error) {
	if d.team == nil || rt == nil {
		return TeamTurn{}, nil, nil
	}
	return d.team.Prepare(ctx, src, rt.Session())
}

func (d *Dispatcher) runSourceTurn(ctx context.Context, src, msgType, initialInput string, rt agrt.Runtime) (string, error) {
	nr, isNative := rt.(*NativeRuntime)
	if !isNative {
		state, err := d.startRun(ctx, src, msgType, initialInput, rt)
		if err != nil {
			return d.agentID, err
		}
		runCtx := withRuntimeTurn(ctx, state, src)
		err = rt.Send(runCtx, initialInput)
		_ = d.finishRun(ctx, state, err)
		return d.agentID, err
	}

	currentInput := initialInput
	lastSpeakerID := d.agentID
	for {
		teamTurn, release, err := d.prepareTeamTurn(ctx, src, nr)
		if err != nil {
			return lastSpeakerID, err
		}
		runSource := src
		runAgentID := d.agentID
		runInput := currentInput
		runRT := nr
		if release != nil {
			runSource = teamTurn.RuntimeSource
			runAgentID = teamTurn.SpeakerAgentID
			runRT = NewNativeRuntime(d.teamAgent(teamTurn, nr.Session()))
			runRT.source = runSource
			if strings.TrimSpace(teamTurn.Prompt) != "" {
				runInput = teamTurn.Prompt
			}
		}
		lastSpeakerID = runAgentID
		state, err := d.startRunForAgent(ctx, runSource, runRT.Session().ID(), runAgentID, msgType, runInput)
		if err != nil {
			if release != nil {
				release()
			}
			return lastSpeakerID, err
		}
		runCtx := withRuntimeTurn(ctx, state, runSource)
		if release != nil {
			runCtx = withTeamTurn(runCtx, teamTurn)
		}
		restore := d.setActiveRuntime(src, runRT)
		err = d.runWithStatus(runCtx, src, msgType, runInput, runRT)
		restore()
		_ = d.finishRun(ctx, state, err)
		if release != nil {
			d.team.Complete(runCtx, teamTurn, d.lastAssistantOutput(runRT), err)
			release()
		}
		if err != nil {
			return lastSpeakerID, err
		}
		handoff, ok := d.team.Pending(src)
		if !ok && release != nil {
			handoff, ok, err = d.team.AutoSchedule(ctx, src, teamTurn)
			if err != nil {
				return lastSpeakerID, err
			}
		}
		if !ok {
			return lastSpeakerID, nil
		}
		currentInput = handoff.Prompt
	}
}

func (d *Dispatcher) teamAgent(turn TeamTurn, sess *session.Session) *Agent {
	if sess == nil {
		return nil
	}
	desc := d.runtimeDescriptor(turn)
	provider, sel := d.runtimeProviders(desc.Model)
	prompt := strings.TrimSpace(strings.Join([]string{d.deps.Prompt, desc.Prompt, teamSpecialistPrompt(turn)}, "\n\n"))
	agentOpts := []Option{
		WithBus(d.deps.Bus),
		WithSel(sel),
		WithHooks(d.deps.Hooks),
		WithToolGuard(d.deps.ToolGuard),
		WithPrompt(prompt),
		WithConfig(d.deps.Config),
		WithRuntimeDB(d.deps.RuntimeDB),
		WithMemoryStore(d.deps.Memory),
		WithProvider(provider),
		WithSoulPath(desc.SoulPath),
		WithSubAgent(false),
	}
	if d.deps.CronTool != nil {
		agentOpts = append(agentOpts, WithCronTool(d.deps.CronTool))
	}
	a := New(turn.SpeakerAgentID, provider, sess, agentOpts...)
	if d.skillLoader != nil {
		skill.RegisterTools(a.Tools(), d.skillLoader)
	}
	return a
}

func (d *Dispatcher) runtimeDescriptor(turn TeamTurn) AgentDescriptor {
	runtimeAgentID := strings.TrimSpace(turn.RuntimeAgentID)
	if runtimeAgentID == "" {
		runtimeAgentID = turn.SpeakerAgentID
	}
	if d.registry != nil {
		if state := d.registry.Get(runtimeAgentID); state != nil {
			return state.Descriptor
		}
	}
	return AgentDescriptor{}
}

func (d *Dispatcher) runtimeProviders(modelName string) (llm.Provider, *llm.Sel) {
	modelName = strings.TrimSpace(modelName)
	switch modelName {
	case "", "default":
		return d.deps.Provider, d.deps.Sel
	case "cheap":
		if d.deps.Sel != nil {
			return d.deps.Sel.P("cheap"), nil
		}
		return d.deps.Provider, d.deps.Sel
	}
	cfg := d.deps.Config
	if !config.ResolveModel(&cfg, modelName) {
		return d.deps.Provider, d.deps.Sel
	}
	provider, err := llm.NewProvider(llm.Config{
		Provider:  cfg.Active.Provider,
		APIKey:    cfg.Active.APIKey,
		BaseURL:   cfg.Active.BaseURL,
		Model:     cfg.Active.Model,
		Headers:   cfg.Active.Headers,
		MaxTokens: cfg.Active.MaxTokens,
		Reasoning: cfg.Active.Reasoning,
	})
	if err != nil {
		return d.deps.Provider, d.deps.Sel
	}
	return provider, nil
}

func teamSpecialistPrompt(turn TeamTurn) string {
	var lines []string
	if role := strings.TrimSpace(turn.SpeakerRole); role != "" {
		lines = append(lines, "Current specialist role: "+role)
	}
	if desc := strings.TrimSpace(turn.SpeakerRoleDesc); desc != "" {
		lines = append(lines, "Current specialist scope: "+desc)
	}
	if profile := strings.TrimSpace(turn.SpeakerProfile); profile != "" {
		lines = append(lines, "Current specialist profile hint: "+profile)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
