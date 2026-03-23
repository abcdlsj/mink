package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"time"

	"github.com/abcdlsj/mink/bus"
)

var taskNames = []string{
	"rain", "wind", "snow", "mist", "dawn",
	"dusk", "wave", "glow", "leaf", "star",
}

func randTaskName() string {
	adj := []string{"quick", "lazy", "warm", "cool", "bright", "soft", "deep", "light", "fresh", "clear"}
	return adj[rand.Intn(len(adj))] + "-" + taskNames[rand.Intn(len(taskNames))]
}

type Background struct {
	bus     *bus.Bus
	agentID string
	timeout int
}

type backgroundParams struct {
	Cmd string `json:"cmd"`
	Cwd string `json:"cwd,omitempty"`
}

func NewBackground(b *bus.Bus, agentID string) *Background {
	return &Background{bus: b, agentID: agentID, timeout: 1800}
}

func (b *Background) SetTimeout(seconds int) {
	b.timeout = seconds
}

func (b *Background) Name() string { return "background" }
func (b *Background) Desc() string {
	return "Run a command in background. Returns immediately with task_id. Result will be sent back when done."
}

func (b *Background) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cmd": map[string]any{
				"type":        "string",
				"description": "Command to execute in background",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory (optional)",
			},
		},
		"required": []string{"cmd"},
	}
}

func (b *Background) Run(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := parseBackgroundParams(args)
	if err != nil {
		return "", err
	}

	taskID := "[task]" + randTaskName()
	source := bus.SourceFrom(ctx)

	b.publishTaskStart(taskID, source, params.Cmd)
	go b.runTask(taskID, source, params)

	return fmt.Sprintf("Task %s started in background. You'll receive the result when it's done.", taskID), nil
}

func parseBackgroundParams(args json.RawMessage) (backgroundParams, error) {
	var params backgroundParams
	if err := json.Unmarshal(args, &params); err != nil {
		return backgroundParams{}, err
	}
	if params.Cmd == "" {
		return backgroundParams{}, fmt.Errorf("cmd is required")
	}
	return params, nil
}

func (b *Background) publishTaskStart(taskID, source, cmd string) {
	to := source
	if to == "" {
		to = b.agentID
	}
	_ = b.bus.Pub(bus.Msg{
		Type: bus.TypeTaskStart,
		From: b.agentID,
		To:   to,
		Payload: map[string]string{
			"task_id": taskID,
			"cmd":     cmd,
			"source":  source,
		},
	})
}

func (b *Background) runTask(taskID, source string, params backgroundParams) {
	timeout := time.Duration(b.timeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", params.Cmd)
	if params.Cwd != "" {
		cmd.Dir = params.Cwd
	}

	output, err := cmd.CombinedOutput()
	b.publishTaskDone(taskID, source, string(output), err, ctx.Err())
}

func (b *Background) publishTaskDone(taskID, source, output string, err error, ctxErr error) {
	result := map[string]string{
		"task_id": taskID,
		"output":  output,
		"status":  "ok",
		"source":  source,
	}
	if err != nil {
		result["status"] = "error"
		if ctxErr == context.DeadlineExceeded {
			result["error"] = fmt.Sprintf("timeout after %ds", b.timeout)
		} else {
			result["error"] = err.Error()
		}
	}

	_ = b.bus.Pub(bus.Msg{
		Type:    bus.TypeTaskDone,
		From:    taskID,
		To:      b.agentID,
		Payload: result,
	})
}
