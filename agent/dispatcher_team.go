package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/internal/xstr"
	"github.com/abcdlsj/mink/llm"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
)

func (d *Dispatcher) prepareTeamTurn(ctx context.Context, src string, rt *NativeRuntime) (TeamTurn, func(), error) {
	if d.team == nil || rt == nil {
		return TeamTurn{}, nil, nil
	}
	return d.team.Prepare(ctx, src, rt.Session())
}

func (d *Dispatcher) runSourceTurn(ctx context.Context, src, msgType, initialInput string, rt *NativeRuntime) (string, error) {
	currentInput := initialInput
	lastSpeakerID := d.agentID
	for {
		teamTurn, release, err := d.prepareTeamTurn(ctx, src, rt)
		if err != nil {
			return lastSpeakerID, err
		}
		runSource := src
		runAgentID := d.agentID
		runInput := currentInput
		runRT := rt
		if release != nil {
			runSource = teamTurn.RuntimeSource
			runAgentID = teamTurn.SpeakerAgentID
			runRT = NewNativeRuntime(d.teamAgent(teamTurn, rt.Session()))
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
		if !ok {
			if release != nil {
				handoff, ok, err = d.team.AutoSchedule(ctx, src, teamTurn)
				if err != nil {
					return lastSpeakerID, err
				}
			}
		}
		if !ok {
			return lastSpeakerID, nil
		}
		currentInput = handoff.Prompt
	}
}

func (d *Dispatcher) handleTeamMention(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "invalid mention payload"}}, nil
	}
	src := payload["source"]
	targetAgentID := payload["agent_id"]
	question := payload["question"]
	if src == "" || targetAgentID == "" || question == "" {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source, agent_id, question are required"}}, nil
	}
	if d.team == nil {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "team runtime unavailable"}}, nil
	}
	if _, ok := d.team.Binding(src); !ok {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source is not in active team thread"}}, nil
	}
	d.team.Schedule(src, targetAgentID, question)
	return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"status": "ok"}}, nil
}

func (d *Dispatcher) handleTeamInvite(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "invalid invite payload"}}, nil
	}
	src := payload["source"]
	agentID := payload["agent_id"]
	roleName := payload["role_name"]
	roleDescription := payload["role_description"]
	task := payload["task"]
	if src == "" || agentID == "" {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source and agent_id are required"}}, nil
	}
	if roleName == "" {
		roleName = agentID
	}
	if d.team == nil || d.rt == nil {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "team runtime unavailable"}}, nil
	}
	binding, ok := d.team.Binding(src)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source is not in active team thread"}}, nil
	}
	if err := d.rt.AddTeamMember(ctx, binding.TeamID, agentID, roleName, roleDescription, "persistent"); err != nil {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	if identity, err := d.rt.GetAgentIdentity(ctx, agentID); err == nil && identity.AgentID == "" {
		if err := d.rt.UpsertAgentIdentity(ctx, agentID, roleName, xstr.FirstNonEmpty(roleDescription, roleName), "team:"+binding.TeamID); err != nil {
			return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
		}
	}
	if strings.TrimSpace(task) != "" {
		d.team.Schedule(src, agentID, task)
	}
	return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"status": "ok"}}, nil
}

func (d *Dispatcher) handleTeamSpawn(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]any)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "invalid spawn payload"}}, nil
	}
	src, _ := payload["source"].(string)
	roleName, _ := payload["role_name"].(string)
	roleDescription, _ := payload["role_description"].(string)
	profileHint, _ := payload["profile_hint"].(string)
	task, _ := payload["task"].(string)
	requestedAgentID, _ := payload["agent_id"].(string)
	var capabilities []string
	if rawCaps, ok := payload["capabilities"].([]any); ok {
		for _, cap := range rawCaps {
			if s, ok := cap.(string); ok && strings.TrimSpace(s) != "" {
				capabilities = append(capabilities, strings.TrimSpace(s))
			}
		}
	}
	if src == "" || roleName == "" || roleDescription == "" {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source, role_name, role_description are required"}}, nil
	}
	if d.team == nil || d.rt == nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "team runtime unavailable"}}, nil
	}
	binding, ok := d.team.Binding(src)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source is not in active team thread"}}, nil
	}
	runtimeAgentID, err := d.resolveSpecialistRuntimeAgent(requestedAgentID, profileHint, capabilities)
	if err != nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	agentID := d.specialistAlias(ctx, binding.TeamID, roleName)
	if err := d.rt.AddTeamMemberWithProfile(ctx, binding.TeamID, agentID, roleName, roleDescription, "ephemeral", rtsqlite.TeamMemberProfile{
		RuntimeAgentID: runtimeAgentID,
		ProfileHint:    profileHint,
	}); err != nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	profile := strings.TrimSpace(profileHint)
	if profile == "" {
		profile = strings.TrimSpace(roleDescription)
	}
	if err := d.rt.UpsertAgentIdentity(ctx, agentID, roleName, profile, "team:"+binding.TeamID); err != nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	if strings.TrimSpace(task) != "" {
		d.team.Schedule(src, agentID, task)
	}
	return bus.Msg{
		Type:    bus.TypeTeamSpawn,
		From:    d.agentID,
		To:      m.From,
		ReplyTo: m.ID,
		Payload: map[string]string{
			"status":           "ok",
			"agent_id":         agentID,
			"runtime_agent_id": runtimeAgentID,
		},
	}, nil
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

func (d *Dispatcher) resolveSpecialistRuntimeAgent(requestedAgentID, profileHint string, capabilities []string) (string, error) {
	if d.registry != nil {
		if requestedAgentID != "" {
			if state := d.registry.Get(requestedAgentID); state != nil {
				return state.Descriptor.ID, nil
			}
		}
		if len(capabilities) > 0 {
			state, err := d.registry.Route(capabilities)
			if err == nil {
				return state.Descriptor.ID, nil
			}
		}
		if candidate := d.matchRegistryAgent(profileHint); candidate != "" {
			return candidate, nil
		}
		available := d.registry.Available()
		if len(available) > 0 {
			return available[0].Descriptor.ID, nil
		}
	}
	if strings.TrimSpace(requestedAgentID) != "" {
		return strings.TrimSpace(requestedAgentID), nil
	}
	if strings.TrimSpace(d.agentID) != "" {
		return d.agentID, nil
	}
	return bus.AddrAgentMain, nil
}

func (d *Dispatcher) matchRegistryAgent(profileHint string) string {
	if d.registry == nil {
		return ""
	}
	hint := strings.ToLower(strings.TrimSpace(profileHint))
	if hint == "" {
		return ""
	}
	if state := d.registry.Get(hint); state != nil {
		return state.Descriptor.ID
	}
	for _, state := range d.registry.All() {
		desc := state.Descriptor
		if strings.EqualFold(desc.Name, hint) || strings.EqualFold(desc.Model, hint) {
			return desc.ID
		}
		for _, cap := range desc.Capabilities {
			if strings.EqualFold(cap, hint) {
				return desc.ID
			}
		}
	}
	for _, state := range d.registry.All() {
		desc := state.Descriptor
		if strings.Contains(strings.ToLower(desc.ID), hint) || strings.Contains(strings.ToLower(desc.Name), hint) {
			return desc.ID
		}
		for _, cap := range desc.Capabilities {
			if strings.Contains(strings.ToLower(cap), hint) {
				return desc.ID
			}
		}
	}
	return ""
}

func (d *Dispatcher) specialistAlias(ctx context.Context, teamID, roleName string) string {
	base := sanitizeAlias(roleName)
	if base == "" {
		base = "specialist"
	}
	prefix := "agent:team:" + shortAlias(teamID) + ":"
	members, err := d.rt.ListTeamMembers(ctx, teamID)
	if err != nil {
		return prefix + base
	}
	existing := make(map[string]struct{}, len(members))
	for _, member := range members {
		existing[member.AgentID] = struct{}{}
	}
	candidate := prefix + base
	if _, ok := existing[candidate]; !ok {
		return candidate
	}
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s%s-%d", prefix, base, i)
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
}

func sanitizeAlias(roleName string) string {
	roleName = strings.ToLower(strings.TrimSpace(roleName))
	var b strings.Builder
	lastDash := false
	for _, r := range roleName {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func shortAlias(teamID string) string {
	teamID = strings.TrimSpace(teamID)
	if len(teamID) <= 8 {
		return teamID
	}
	return teamID[len(teamID)-8:]
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
