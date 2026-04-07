package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/mink/bus"
)

type Delegate struct {
	bus      *bus.Bus
	agentID  string
}

func NewDelegate(b *bus.Bus, agentID string) *Delegate {
	return &Delegate{bus: b, agentID: agentID}
}

func (d *Delegate) Name() string { return "delegate" }
func (d *Delegate) Desc() string {
	return "Delegate a task to a peer agent. Specify either a target agent ID or required capabilities for automatic routing. Returns when the peer completes the task."
}

func (d *Delegate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "Clear description of the task to delegate",
			},
			"target": map[string]any{
				"type":        "string",
				"description": "Target agent ID (e.g. 'agent:researcher'). If empty, capabilities are used for routing.",
			},
			"capabilities": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Required capabilities for automatic agent routing (used when target is empty)",
			},
		},
		"required": []string{"task"},
	}
}

func (d *Delegate) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Task         string   `json:"task"`
		Target       string   `json:"target"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Task == "" {
		return "", fmt.Errorf("task is required")
	}

	payload := map[string]any{
		"description":  params.Task,
		"capabilities": params.Capabilities,
		"target_agent": params.Target,
		"reply_to":     d.agentID,
		"depth":        0,
	}

	to := bus.AddrAgentMain
	if params.Target != "" {
		to = params.Target
	}

	resp, err := d.bus.Req(ctx, bus.Msg{
		Type:    bus.TypeDelegate,
		From:    d.agentID,
		To:      to,
		Payload: payload,
	})
	if err != nil {
		return "", fmt.Errorf("delegate failed: %w", err)
	}

	if result, ok := resp.Payload.(map[string]string); ok {
		if errMsg := result["error"]; errMsg != "" {
			return "", fmt.Errorf("delegate error: %s", errMsg)
		}
		if output := result["output"]; output != "" {
			return output, nil
		}
	}
	if s, ok := resp.Payload.(string); ok {
		return s, nil
	}
	return "delegation completed", nil
}
