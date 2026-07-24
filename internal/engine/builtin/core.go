package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	"github.com/abcdlsj/sumi/internal/engine"
	"github.com/abcdlsj/sumi/internal/provider"
	"github.com/abcdlsj/sumi/internal/tool"
)

const maxModelTurns = 16

type Core struct {
	assembler engine.ContextAssembler
	provider  provider.Client
	gateway   *tool.Gateway
	timeout   time.Duration
	maxOutput uint64
}

func NewCore(assembler engine.ContextAssembler, model provider.Client, gateway *tool.Gateway, timeout time.Duration, maxOutput uint64) (*Core, error) {
	if model == nil || gateway == nil || timeout <= 0 || maxOutput == 0 {
		return nil, errors.New("builtin core boundaries are required")
	}
	return &Core{assembler: assembler, provider: model, gateway: gateway, timeout: timeout, maxOutput: maxOutput}, nil
}

func (c *Core) Execute(ctx context.Context, exec computerruntime.Execution) (computerruntime.Completion, error) {
	in, err := c.assembler.Build(exec)
	if err != nil {
		return computerruntime.Completion{}, err
	}
	prompt, err := in.Prompt()
	if err != nil {
		return computerruntime.Completion{}, err
	}
	rctx := tool.RunContext{
		AgentID: exec.AgentID, ComputerID: exec.ComputerID, RunID: exec.RunID,
		Attempt: exec.Attempt, Fence: exec.Fence, PlacementDesiredRevision: exec.PlacementDesiredRevision,
	}
	defer c.gateway.Finish(rctx)
	req := provider.Request{
		System:   engine.SystemContract + "\n\n" + prompt.Sections[1].Text,
		Messages: []provider.Message{{Role: provider.RoleUser, Text: contextText(prompt.Sections[2:])}},
		Tools:    c.gateway.Definitions(),
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var usage computerruntime.Usage
	for range maxModelTurns {
		resp, err := c.provider.Complete(ctx, req)
		if err != nil {
			return computerruntime.Completion{}, err
		}
		usage.InputUnits += resp.Usage.InputUnits
		usage.OutputUnits += resp.Usage.OutputUnits
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Text) == "" {
				return computerruntime.Completion{}, errors.New("provider returned an empty final response")
			}
			if uint64(len(resp.Text)) > c.maxOutput {
				return computerruntime.Completion{}, errors.New("provider final response exceeds output bound")
			}
			return computerruntime.Completion{Outcome: computerruntime.OutcomeSucceeded, Body: resp.Text, Usage: usage}, nil
		}
		req.Messages = append(req.Messages, provider.Message{Role: provider.RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
			result, err := c.gateway.Execute(ctx, rctx, call)
			if err != nil {
				result, _ = json.Marshal(map[string]any{"ok": false, "error": stableToolError(err)})
			}
			req.Messages = append(req.Messages, provider.Message{Role: provider.RoleTool, ToolCallID: call.ID, Text: string(result)})
		}
	}
	return computerruntime.Completion{}, errors.New("builtin model loop exceeded turn limit")
}

func (c *Core) Close() error { return nil }

func contextText(sections []engine.Section) string {
	var builder strings.Builder
	for _, section := range sections {
		fmt.Fprintf(&builder, "<sumi:%s source=%q>\n%s\n</sumi:%s>\n\n", section.Name, section.Source, section.Text, section.Name)
	}
	return builder.String()
}

func stableToolError(err error) string {
	switch {
	case errors.Is(err, tool.ErrDenied):
		return "denied"
	case errors.Is(err, tool.ErrInvalidCall):
		return "invalid_call"
	case errors.Is(err, tool.ErrBudgetExceeded):
		return "budget_exceeded"
	case errors.Is(err, tool.ErrIdempotencyConflict):
		return "idempotency_conflict"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "tool_failed"
	}
}

var _ computerruntime.Engine = (*Core)(nil)
