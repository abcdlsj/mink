package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/abcdlsj/mink/bus"
)

type Spawn struct {
	bus      *bus.Bus
	parentID string
}

func NewSpawn(b *bus.Bus, parentID string) *Spawn {
	return &Spawn{bus: b, parentID: parentID}
}

func (s *Spawn) Name() string { return "spawn" }
func (s *Spawn) Desc() string {
	return "Spawn a new agent to handle a subtask. Use direct_output to let the agent respond directly to user, or keep silent to process its result yourself."
}

func (s *Spawn) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "Clear description of the task for the new agent",
			},
			"share_context": map[string]any{
				"type":        "boolean",
				"description": "Whether to share conversation context with the new agent (default: false)",
			},
			"direct_output": map[string]any{
				"type":        "boolean",
				"description": "If true, agent output is shown directly to user; if false (default), output is returned to you for processing",
			},
		},
		"required": []string{"task"},
	}
}

func (s *Spawn) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Task         string `json:"task"`
		ShareContext bool   `json:"share_context"`
		DirectOutput bool   `json:"direct_output"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	if params.Task == "" {
		return "", fmt.Errorf("task is required")
	}

	resp, err := s.bus.Req(ctx, bus.Msg{
		Type: bus.TypeAgentSpawn,
		From: s.parentID,
		To:   bus.AddrSystemSup,
		Payload: map[string]any{
			"task":          params.Task,
			"share_context": params.ShareContext,
			"direct_output": params.DirectOutput,
		},
	})
	if err != nil {
		return "", fmt.Errorf("spawn failed: %w", err)
	}

	payload, ok := resp.Payload.(map[string]string)
	if !ok {
		return "", fmt.Errorf("invalid spawn response")
	}

	childID := payload["agent_id"]
	if childID == "" {
		return "", fmt.Errorf("no agent_id in response")
	}

	// 等待子 agent 完成
	ch := make(chan bus.Msg, 8)
	s.bus.Subscribe(bus.TypeAgentDone, ch)
	defer s.bus.Unsubscribe(bus.TypeAgentDone, ch)

	timeout := time.After(10 * time.Minute)
	for {
		select {
		case m := <-ch:
			if m.From != childID {
				continue
			}
			if p, ok := m.Payload.(map[string]string); ok {
				return p["result"], nil
			}
			return "completed", nil
		case <-timeout:
			return "", fmt.Errorf("agent %s timeout", childID)
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}
