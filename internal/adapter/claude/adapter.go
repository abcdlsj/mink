package claude

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/internal/adapter/external"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	"github.com/abcdlsj/sumi/internal/engine"
)

type Adapter struct {
	assembler engine.ContextAssembler
	runner    external.ProcessRunner
}

func New(profile *agentv1.AgentProfile, runtimeSpec *agentv1.AgentRuntimeSpec, hostPolicy string, runner external.ProcessRunner) (*Adapter, error) {
	if profile == nil || runtimeSpec == nil || runtimeSpec.GetEngine() != agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER {
		return nil, errors.New("claude adapter configuration is invalid")
	}
	if err := runner.Validate(); err != nil {
		return nil, err
	}
	return &Adapter{assembler: engine.ContextAssembler{Profile: profile, Runtime: runtimeSpec, HostPolicy: hostPolicy}, runner: runner}, nil
}

func (adapter *Adapter) Execute(ctx context.Context, execution computerruntime.Execution) (computerruntime.Completion, error) {
	input, err := adapter.assembler.Build(execution)
	if err != nil {
		return computerruntime.Completion{}, err
	}
	prompt, err := input.Prompt()
	if err != nil {
		return computerruntime.Completion{}, err
	}
	var result claudeResult
	err = adapter.runner.Run(ctx, external.Binding{
		AgentID: execution.AgentID, ComputerID: execution.ComputerID, RunID: execution.RunID,
		Attempt: execution.Attempt, Fence: execution.Fence, PlacementDesiredRevision: execution.PlacementDesiredRevision,
		Workspace: execution.Workspace,
	}, []byte(prompt.Text()), result.consume)
	if err != nil {
		return computerruntime.Completion{}, err
	}
	return result.completion()
}

func (adapter *Adapter) Close() error { return nil }

type claudeEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Usage   struct {
		InputTokens  uint64 `json:"input_tokens"`
		OutputTokens uint64 `json:"output_tokens"`
	} `json:"usage"`
}

type claudeResult struct {
	body  string
	usage computerruntime.Usage
}

func (result *claudeResult) consume(line []byte) error {
	var event claudeEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return errors.New("claude adapter returned invalid JSONL")
	}
	if event.Type != "result" {
		return nil
	}
	if event.IsError || event.Subtype != "success" {
		return errors.New("claude adapter reported failure")
	}
	result.body = event.Result
	result.usage.InputUnits += event.Usage.InputTokens
	result.usage.OutputUnits += event.Usage.OutputTokens
	return nil
}

func (result claudeResult) completion() (computerruntime.Completion, error) {
	if strings.TrimSpace(result.body) == "" {
		return computerruntime.Completion{}, errors.New("claude adapter returned no final message")
	}
	return computerruntime.Completion{Outcome: computerruntime.OutcomeSucceeded, Body: result.body, Usage: result.usage}, nil
}

var _ computerruntime.Engine = (*Adapter)(nil)
