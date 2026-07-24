package codex

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
	if profile == nil || runtimeSpec == nil || runtimeSpec.GetEngine() != agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER {
		return nil, errors.New("codex adapter configuration is invalid")
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
	var result codexResult
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

type codexEvent struct {
	Type string `json:"type"`
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage struct {
		InputTokens  uint64 `json:"input_tokens"`
		OutputTokens uint64 `json:"output_tokens"`
	} `json:"usage"`
}

type codexResult struct {
	body  string
	usage computerruntime.Usage
}

func (result *codexResult) consume(line []byte) error {
	var event codexEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return errors.New("codex adapter returned invalid JSONL")
	}
	switch event.Type {
	case "item.completed":
		if event.Item.Type == "agent_message" {
			result.body += event.Item.Text
		}
	case "turn.completed":
		result.usage.InputUnits += event.Usage.InputTokens
		result.usage.OutputUnits += event.Usage.OutputTokens
	case "error", "turn.failed":
		return errors.New("codex adapter reported failure")
	}
	return nil
}

func (result codexResult) completion() (computerruntime.Completion, error) {
	if strings.TrimSpace(result.body) == "" {
		return computerruntime.Completion{}, errors.New("codex adapter returned no final message")
	}
	return computerruntime.Completion{Outcome: computerruntime.OutcomeSucceeded, Body: result.body, Usage: result.usage}, nil
}

var _ computerruntime.Engine = (*Adapter)(nil)
