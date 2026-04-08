package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/mink/bus"
)

type TeamSpawnSpecialist struct {
	bus     *bus.Bus
	agentID string
}

func NewTeamSpawnSpecialist(b *bus.Bus, agentID string) *TeamSpawnSpecialist {
	return &TeamSpawnSpecialist{bus: b, agentID: agentID}
}

func (t *TeamSpawnSpecialist) Name() string { return "spawn_specialist" }

func (t *TeamSpawnSpecialist) Desc() string {
	return "Create a dynamic specialist role in the current team, bind it to the best matching runtime profile, and optionally schedule its next visible turn."
}

func (t *TeamSpawnSpecialist) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role_name": map[string]any{
				"type":        "string",
				"description": "Visible specialist role name inside the team",
			},
			"role_description": map[string]any{
				"type":        "string",
				"description": "Responsibility and scope for this specialist",
			},
			"profile_hint": map[string]any{
				"type":        "string",
				"description": "Optional hint used to choose the runtime profile or model",
			},
			"capabilities": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Optional capability constraints for runtime profile routing",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "Optional immediate task for the spawned specialist's next turn",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Optional explicit runtime agent identity to bind behind this specialist",
			},
		},
		"required": []string{"role_name", "role_description"},
	}
}

func (t *TeamSpawnSpecialist) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		RoleName        string   `json:"role_name"`
		RoleDescription string   `json:"role_description"`
		ProfileHint     string   `json:"profile_hint"`
		Capabilities    []string `json:"capabilities"`
		Task            string   `json:"task"`
		AgentID         string   `json:"agent_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.RoleName == "" || params.RoleDescription == "" {
		return "", fmt.Errorf("role_name and role_description are required")
	}

	resp, err := t.bus.Req(ctx, bus.Msg{
		Type: bus.TypeTeamSpawn,
		From: t.agentID,
		To:   bus.AddrAgentMain,
		Payload: map[string]any{
			"source":           bus.SourceFrom(ctx),
			"role_name":        params.RoleName,
			"role_description": params.RoleDescription,
			"profile_hint":     params.ProfileHint,
			"capabilities":     params.Capabilities,
			"task":             params.Task,
			"agent_id":         params.AgentID,
			"requested":        t.agentID,
		},
	})
	if err != nil {
		return "", fmt.Errorf("spawn_specialist failed: %w", err)
	}
	ack, ok := resp.Payload.(map[string]string)
	if !ok {
		return "", fmt.Errorf("invalid spawn_specialist response")
	}
	if errMsg := ack["error"]; errMsg != "" {
		return "", fmt.Errorf("%s", errMsg)
	}
	if alias := ack["agent_id"]; alias != "" {
		if runtimeID := ack["runtime_agent_id"]; runtimeID != "" && runtimeID != alias {
			return fmt.Sprintf("spawned %s backed by %s", alias, runtimeID), nil
		}
		return fmt.Sprintf("spawned %s", alias), nil
	}
	return "spawned specialist", nil
}
