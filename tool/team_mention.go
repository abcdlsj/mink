package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/mink/bus"
)

type TeamMention struct {
	bus     *bus.Bus
	agentID string
}

func NewTeamMention(b *bus.Bus, agentID string) *TeamMention {
	return &TeamMention{bus: b, agentID: agentID}
}

func (t *TeamMention) Name() string { return "mention" }

func (t *TeamMention) Desc() string {
	return "Hand off the next visible team turn to a specific persistent agent identity."
}

func (t *TeamMention) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Target agent identity for the next team turn",
			},
			"question": map[string]any{
				"type":        "string",
				"description": "Question or instruction for the target agent's next team turn",
			},
		},
		"required": []string{"agent_id", "question"},
	}
}

func (t *TeamMention) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		AgentID  string `json:"agent_id"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.AgentID == "" || params.Question == "" {
		return "", fmt.Errorf("agent_id and question are required")
	}

	resp, err := t.bus.Req(ctx, bus.Msg{
		Type: bus.TypeTeamMention,
		From: t.agentID,
		To:   bus.AddrAgentMain,
		Payload: map[string]string{
			"source":    bus.SourceFrom(ctx),
			"agent_id":  params.AgentID,
			"question":  params.Question,
			"requested": t.agentID,
		},
	})
	if err != nil {
		return "", fmt.Errorf("mention failed: %w", err)
	}
	if ack, ok := resp.Payload.(map[string]string); ok {
		if errMsg := ack["error"]; errMsg != "" {
			return "", fmt.Errorf("%s", errMsg)
		}
	}
	return fmt.Sprintf("scheduled next team turn for %s", params.AgentID), nil
}
