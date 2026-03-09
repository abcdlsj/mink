package tool

import (
	"context"
	"encoding/json"
	"fmt"

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
	return "Run a subtask with a child agent. Returns the child result when it completes; use direct_output to also show the child output directly to the user."
}

func (s *Spawn) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "Clear description of the task for the child agent",
			},
			"share_context": map[string]any{
				"type":        "boolean",
				"description": "Whether to share conversation context with the child agent (default: false)",
			},
			"direct_output": map[string]any{
				"type":        "boolean",
				"description": "If true, child output is shown directly to the user while still returning the final result to you",
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
		Type: bus.TypeSubtaskRun,
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
	if result := payload["result"]; result != "" {
		return result, nil
	}
	return "completed", nil
}
