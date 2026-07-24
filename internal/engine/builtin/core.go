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

func (core *Core) Execute(ctx context.Context, execution computerruntime.Execution) (computerruntime.Completion, error) {
	runInput, err := core.assembler.Build(execution)
	if err != nil {
		return computerruntime.Completion{}, err
	}
	prompt, err := runInput.Prompt()
	if err != nil {
		return computerruntime.Completion{}, err
	}
	runContext := tool.RunContext{
		AgentID: execution.AgentID, ComputerID: execution.ComputerID, RunID: execution.RunID,
		Attempt: execution.Attempt, Fence: execution.Fence, PlacementDesiredRevision: execution.PlacementDesiredRevision,
	}
	defer core.gateway.Finish(runContext)
	request := provider.Request{
		System:   engine.SystemContract + "\n\n" + prompt.Sections[1].Text,
		Messages: []provider.Message{{Role: provider.RoleUser, Text: contextText(prompt.Sections[2:])}},
		Tools:    core.gateway.Definitions(),
	}
	runCtx, cancel := context.WithTimeout(ctx, core.timeout)
	defer cancel()
	var usage computerruntime.Usage
	for range maxModelTurns {
		response, err := core.provider.Complete(runCtx, request)
		if err != nil {
			return computerruntime.Completion{}, err
		}
		usage.InputUnits += response.Usage.InputUnits
		usage.OutputUnits += response.Usage.OutputUnits
		if len(response.ToolCalls) == 0 {
			if strings.TrimSpace(response.Text) == "" {
				return computerruntime.Completion{}, errors.New("provider returned an empty final response")
			}
			if uint64(len(response.Text)) > core.maxOutput {
				return computerruntime.Completion{}, errors.New("provider final response exceeds output bound")
			}
			return computerruntime.Completion{Outcome: computerruntime.OutcomeSucceeded, Body: response.Text, Usage: usage}, nil
		}
		request.Messages = append(request.Messages, provider.Message{Role: provider.RoleAssistant, Text: response.Text, ToolCalls: response.ToolCalls})
		for _, call := range response.ToolCalls {
			result, executeErr := core.gateway.Execute(runCtx, runContext, call)
			if executeErr != nil {
				result, _ = json.Marshal(map[string]any{"ok": false, "error": stableToolError(executeErr)})
			}
			request.Messages = append(request.Messages, provider.Message{Role: provider.RoleTool, ToolCallID: call.ID, Text: string(result)})
		}
	}
	return computerruntime.Completion{}, errors.New("builtin model loop exceeded turn limit")
}

func (core *Core) Close() error { return nil }

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
