package agent

import (
	"context"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/internal/xstr"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func (d *Dispatcher) handleTeamMention(_ context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return teamError(bus.TypeTeamMention, d.agentID, m, "invalid mention payload"), nil
	}
	src := payload["source"]
	targetAgentID := payload["agent_id"]
	question := payload["question"]
	if src == "" || targetAgentID == "" || question == "" {
		return teamError(bus.TypeTeamMention, d.agentID, m, "source, agent_id, question are required"), nil
	}
	if d.team == nil {
		return teamError(bus.TypeTeamMention, d.agentID, m, "team runtime unavailable"), nil
	}
	if _, ok := d.team.Binding(src); !ok {
		return teamError(bus.TypeTeamMention, d.agentID, m, "source is not in active team thread"), nil
	}
	d.team.Schedule(src, targetAgentID, question)
	return teamOK(bus.TypeTeamMention, d.agentID, m), nil
}

func (d *Dispatcher) handleTeamInvite(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return teamError(bus.TypeTeamInvite, d.agentID, m, "invalid invite payload"), nil
	}
	src := payload["source"]
	agentID := payload["agent_id"]
	roleName := payload["role_name"]
	roleDescription := payload["role_description"]
	task := payload["task"]
	if src == "" || agentID == "" {
		return teamError(bus.TypeTeamInvite, d.agentID, m, "source and agent_id are required"), nil
	}
	if roleName == "" {
		roleName = agentID
	}
	if d.team == nil || d.rt == nil {
		return teamError(bus.TypeTeamInvite, d.agentID, m, "team runtime unavailable"), nil
	}
	binding, ok := d.team.Binding(src)
	if !ok {
		return teamError(bus.TypeTeamInvite, d.agentID, m, "source is not in active team thread"), nil
	}
	if err := d.rt.AddTeamMember(ctx, binding.TeamID, agentID, roleName, roleDescription, "persistent"); err != nil {
		return teamError(bus.TypeTeamInvite, d.agentID, m, err.Error()), nil
	}
	if identity, err := d.rt.GetAgentIdentity(ctx, agentID); err == nil && identity.AgentID == "" {
		profile := xstr.FirstNonEmpty(roleDescription, roleName)
		if err := d.rt.UpsertAgentIdentity(ctx, agentID, roleName, profile, "team:"+binding.TeamID); err != nil {
			return teamError(bus.TypeTeamInvite, d.agentID, m, err.Error()), nil
		}
	}
	if strings.TrimSpace(task) != "" {
		d.team.Schedule(src, agentID, task)
	}
	return teamOK(bus.TypeTeamInvite, d.agentID, m), nil
}

func (d *Dispatcher) handleTeamSpawn(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]any)
	if !ok {
		return teamError(bus.TypeTeamSpawn, d.agentID, m, "invalid spawn payload"), nil
	}
	src, _ := payload["source"].(string)
	roleName, _ := payload["role_name"].(string)
	roleDescription, _ := payload["role_description"].(string)
	profileHint, _ := payload["profile_hint"].(string)
	task, _ := payload["task"].(string)
	requestedAgentID, _ := payload["agent_id"].(string)
	capabilities := teamSpawnCapabilities(payload["capabilities"])

	if src == "" || roleName == "" || roleDescription == "" {
		return teamError(bus.TypeTeamSpawn, d.agentID, m, "source, role_name, role_description are required"), nil
	}
	if d.team == nil || d.rt == nil {
		return teamError(bus.TypeTeamSpawn, d.agentID, m, "team runtime unavailable"), nil
	}
	binding, ok := d.team.Binding(src)
	if !ok {
		return teamError(bus.TypeTeamSpawn, d.agentID, m, "source is not in active team thread"), nil
	}
	runtimeAgentID, err := d.resolveSpecialistRuntimeAgent(requestedAgentID, profileHint, capabilities)
	if err != nil {
		return teamError(bus.TypeTeamSpawn, d.agentID, m, err.Error()), nil
	}
	agentID := d.specialistAlias(ctx, binding.TeamID, roleName)
	if err := d.rt.AddTeamMemberWithProfile(ctx, binding.TeamID, agentID, roleName, roleDescription, "ephemeral", rtsqlite.TeamMemberProfile{
		RuntimeAgentID: runtimeAgentID,
		ProfileHint:    profileHint,
	}); err != nil {
		return teamError(bus.TypeTeamSpawn, d.agentID, m, err.Error()), nil
	}
	profile := strings.TrimSpace(profileHint)
	if profile == "" {
		profile = strings.TrimSpace(roleDescription)
	}
	if err := d.rt.UpsertAgentIdentity(ctx, agentID, roleName, profile, "team:"+binding.TeamID); err != nil {
		return teamError(bus.TypeTeamSpawn, d.agentID, m, err.Error()), nil
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

func teamSpawnCapabilities(raw any) []string {
	caps, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(caps))
	for _, cap := range caps {
		s, ok := cap.(string)
		if ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func teamError(msgType, from string, m bus.Msg, err string) bus.Msg {
	return bus.Msg{
		Type:    msgType,
		From:    from,
		To:      m.From,
		ReplyTo: m.ID,
		Payload: map[string]string{"error": err},
	}
}

func teamOK(msgType, from string, m bus.Msg) bus.Msg {
	return bus.Msg{
		Type:    msgType,
		From:    from,
		To:      m.From,
		ReplyTo: m.ID,
		Payload: map[string]string{"status": "ok"},
	}
}
