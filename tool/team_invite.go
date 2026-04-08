package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/mink/bus"
)

type TeamInvite struct {
	bus     *bus.Bus
	agentID string
}

func NewTeamInvite(b *bus.Bus, agentID string) *TeamInvite {
	return &TeamInvite{bus: b, agentID: agentID}
}

func (t *TeamInvite) Name() string { return "invite_agent" }

func (t *TeamInvite) Desc() string {
	return "Invite an agent identity into the current team and optionally schedule its next visible turn."
}

func (t *TeamInvite) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Agent identity to invite into the current team",
			},
			"role_name": map[string]any{
				"type":        "string",
				"description": "Role name inside the current team",
			},
			"role_description": map[string]any{
				"type":        "string",
				"description": "Role description inside the current team",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "Optional immediate task to schedule for the invited agent",
			},
		},
		"required": []string{"agent_id"},
	}
}

func (t *TeamInvite) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		AgentID         string `json:"agent_id"`
		RoleName        string `json:"role_name"`
		RoleDescription string `json:"role_description"`
		Task            string `json:"task"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.AgentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}

	resp, err := t.bus.Req(ctx, bus.Msg{
		Type: bus.TypeTeamInvite,
		From: t.agentID,
		To:   bus.AddrAgentMain,
		Payload: map[string]string{
			"source":           bus.SourceFrom(ctx),
			"agent_id":         params.AgentID,
			"role_name":        params.RoleName,
			"role_description": params.RoleDescription,
			"task":             params.Task,
			"requested":        t.agentID,
		},
	})
	if err != nil {
		return "", fmt.Errorf("invite failed: %w", err)
	}
	if ack, ok := resp.Payload.(map[string]string); ok {
		if errMsg := ack["error"]; errMsg != "" {
			return "", fmt.Errorf("%s", errMsg)
		}
	}
	return fmt.Sprintf("invited %s into current team", params.AgentID), nil
}
