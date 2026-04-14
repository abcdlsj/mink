package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
)

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
