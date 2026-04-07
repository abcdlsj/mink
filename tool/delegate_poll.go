package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/abcdlsj/mink/bus"
)

const delegatePollTimeout = 2 * time.Minute

type DelegatePoll struct {
	bus     *bus.Bus
	agentID string
}

func NewDelegatePoll(b *bus.Bus, agentID string) *DelegatePoll {
	return &DelegatePoll{bus: b, agentID: agentID}
}

func (d *DelegatePoll) Name() string { return "delegate_poll" }
func (d *DelegatePoll) Desc() string {
	return "Wait for the result of an async delegation by task_id. Blocks until the result arrives or timeout."
}

func (d *DelegatePoll) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "The task_id returned by the delegate tool",
			},
		},
		"required": []string{"task_id"},
	}
}

func (d *DelegatePoll) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.TaskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	ch := make(chan bus.Msg, 4)
	d.bus.Subscribe(bus.TypeDelegateResult, ch)
	defer d.bus.Unsubscribe(bus.TypeDelegateResult, ch)

	timeout := time.After(delegatePollTimeout)
	for {
		select {
		case m := <-ch:
			if m.ReplyTo != params.TaskID {
				continue
			}
			if result, ok := m.Payload.(map[string]string); ok {
				if errMsg := result["error"]; errMsg != "" {
					return "", fmt.Errorf("delegation failed: %s", errMsg)
				}
				if output := result["output"]; output != "" {
					return output, nil
				}
			}
			return "delegation completed", nil
		case <-timeout:
			return "", fmt.Errorf("delegation timed out after %s", delegatePollTimeout)
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}
