package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	"github.com/abcdlsj/sumi/internal/engine"
	"github.com/abcdlsj/sumi/internal/provider"
	"github.com/abcdlsj/sumi/internal/tool"
)

func TestCoreCompletesProviderToolLoopWithNormalizedUsage(t *testing.T) {
	model := &scriptedProvider{}
	gateway, err := tool.NewGateway(tool.Config{
		Authorizer: allowTools{}, Store: &singleResultStore{},
		Definitions: []tool.Definition{{
			Name: "lookup", Description: "look up one fact", Schema: json.RawMessage(`{"type":"object"}`),
			Capability: "knowledge.search", Scope: "organization", Validate: func(json.RawMessage) error { return nil },
			Execute: func(_ context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
				if run.RunID != "run-1" || call.ID != "call-1" {
					t.Fatalf("tool binding = %+v / %+v", run, call)
				}
				return json.RawMessage(`{"fact":"current"}`), nil
			},
		}},
		Timeout: time.Second, MaxCallsPerRun: 4, MaxArgumentBytes: 1024, MaxResultBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	core, err := NewCore(testAssembler(), model, gateway, time.Second, 1024)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := core.Execute(context.Background(), testExecution())
	if err != nil {
		t.Fatal(err)
	}
	if completion.Outcome != computerruntime.OutcomeSucceeded || completion.Body != "final answer" || completion.Usage.InputUnits != 12 || completion.Usage.OutputUnits != 5 {
		t.Fatalf("completion = %+v", completion)
	}
	if model.calls != 2 {
		t.Fatalf("provider calls = %d", model.calls)
	}
}

func TestCoreReturnsStableToolFailureToProvider(t *testing.T) {
	model := &scriptedProvider{wantToolError: "denied"}
	gateway, err := tool.NewGateway(tool.Config{
		Authorizer: denyAllTools{}, Store: &singleResultStore{},
		Definitions: []tool.Definition{{
			Name: "lookup", Description: "look up one fact", Schema: json.RawMessage(`{"type":"object"}`),
			Capability: "knowledge.search", Scope: "organization", Validate: func(json.RawMessage) error { return nil },
			Execute: func(context.Context, tool.RunContext, provider.ToolCall) (json.RawMessage, error) {
				return nil, errors.New("must not execute")
			},
		}},
		Timeout: time.Second, MaxCallsPerRun: 4, MaxArgumentBytes: 1024, MaxResultBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	core, err := NewCore(testAssembler(), model, gateway, time.Second, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Execute(context.Background(), testExecution()); err != nil {
		t.Fatal(err)
	}
}

type scriptedProvider struct {
	calls         int
	wantToolError string
}

func (model *scriptedProvider) Complete(_ context.Context, request provider.Request) (provider.Response, error) {
	model.calls++
	if model.calls == 1 {
		if len(request.Tools) != 1 || request.Tools[0].Name != "lookup" || len(request.Messages) != 1 {
			return provider.Response{}, errors.New("initial provider request is invalid")
		}
		return provider.Response{
			ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"query":"facts"}`)}},
			Usage:     provider.Usage{InputUnits: 7, OutputUnits: 2},
		}, nil
	}
	if len(request.Messages) != 3 || len(request.Messages[1].ToolCalls) != 1 || request.Messages[2].Role != provider.RoleTool || request.Messages[2].ToolCallID != "call-1" {
		return provider.Response{}, errors.New("provider continuation is incomplete")
	}
	if model.wantToolError != "" {
		var result struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(request.Messages[2].Text), &result); err != nil || result.Error != model.wantToolError {
			return provider.Response{}, errors.New("stable tool error is missing")
		}
	} else if request.Messages[2].Text != `{"fact":"current"}` {
		return provider.Response{}, errors.New("tool result is missing")
	}
	return provider.Response{Text: "final answer", Usage: provider.Usage{InputUnits: 5, OutputUnits: 3}}, nil
}

func testAssembler() engine.ContextAssembler {
	return engine.ContextAssembler{
		Profile: &agentv1.AgentProfile{AgentId: "agent-1", Revision: 1, DisplayName: "Agent", Role: "tester", Mission: "Test the tool loop"},
		Runtime: &agentv1.AgentRuntimeSpec{
			AgentId: "agent-1", Revision: 1, Engine: agentv1.EngineKind_ENGINE_KIND_BUILTIN,
			ProviderProtocol: agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES,
			ProviderEndpoint: "https://provider.invalid", Model: "test",
			SandboxProvider: agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL,
			ToolPolicy:      &agentv1.RuntimeToolPolicy{Knowledge: true},
		},
		HostPolicy: "The test Host is trusted-local.",
	}
}

func testExecution() computerruntime.Execution {
	return computerruntime.Execution{
		AgentID: "agent-1", ComputerID: "computer-1", RunID: "run-1", Attempt: 1, Fence: 2,
		PlacementDesiredRevision: 3, SpaceID: "space-1", BasisTargetSequence: 1, CurrentInput: "Use the lookup tool.",
	}
}

type allowTools struct{}

func (allowTools) Authorize(context.Context, tool.RunContext, string, string) error { return nil }

type denyAllTools struct{}

func (denyAllTools) Authorize(context.Context, tool.RunContext, string, string) error {
	return tool.ErrDenied
}

type singleResultStore struct {
	value tool.StoredResult
	set   bool
}

func (store *singleResultStore) Load(context.Context, tool.RunContext, string) (tool.StoredResult, bool, error) {
	return store.value, store.set, nil
}

func (store *singleResultStore) Save(_ context.Context, _ tool.RunContext, _ string, result tool.StoredResult) error {
	store.value = tool.StoredResult{RequestHash: result.RequestHash, Result: append(json.RawMessage(nil), result.Result...)}
	store.set = true
	return nil
}
