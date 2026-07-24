package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/provider"
)

func TestGatewayValidatesAuthorizesBoundsAndPersistsIdempotentResults(t *testing.T) {
	store := &memoryResultStore{values: make(map[string]StoredResult)}
	authorizer := &testAuthorizer{}
	executions := 0
	gateway, err := NewGateway(Config{
		Authorizer: authorizer,
		Store:      store,
		Definitions: []Definition{{
			Name: "echo", Description: "echo one bounded value", Schema: json.RawMessage(`{"type":"object"}`),
			Capability: "echo", Scope: "run", Validate: func(arguments json.RawMessage) error {
				if string(arguments) != `{"value":"ok"}` && string(arguments) != `{"value":"other"}` {
					return errors.New("unexpected value")
				}
				return nil
			},
			Execute: func(_ context.Context, _ RunContext, call provider.ToolCall) (json.RawMessage, error) {
				executions++
				return json.RawMessage(`{"ok":true,"call_id":"` + call.ID + `"}`), nil
			},
		}},
		Timeout: 100 * time.Millisecond, MaxCallsPerRun: 1, MaxArgumentBytes: 128, MaxResultBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := testRunContext()
	call := provider.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"value":"ok"}`)}
	first, err := gateway.Execute(context.Background(), run, call)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gateway.Execute(context.Background(), run, call)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || executions != 1 || authorizer.calls != 1 {
		t.Fatalf("idempotent results = %s / %s, executions=%d authorization=%d", first, second, executions, authorizer.calls)
	}

	conflict := call
	conflict.Arguments = json.RawMessage(`{"value":"other"}`)
	if _, err := gateway.Execute(context.Background(), run, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("invalid conflicting call error = %v", err)
	}
	secondCall := provider.ToolCall{ID: "call-2", Name: "echo", Arguments: call.Arguments}
	if _, err := gateway.Execute(context.Background(), run, secondCall); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("budget error = %v", err)
	}
	gateway.Finish(run)
	if _, err := gateway.Execute(context.Background(), run, secondCall); err != nil {
		t.Fatalf("new execution lifetime after Finish: %v", err)
	}
}

func TestGatewayRejectsUnknownOversizedDeniedAndTimedOutCalls(t *testing.T) {
	store := &memoryResultStore{values: make(map[string]StoredResult)}
	authorizer := &testAuthorizer{err: errors.New("no grant")}
	gateway, err := NewGateway(Config{
		Authorizer: authorizer,
		Store:      store,
		Definitions: []Definition{{
			Name: "slow", Description: "wait", Schema: json.RawMessage(`{"type":"object"}`),
			Capability: "slow", Scope: "run", Validate: func(json.RawMessage) error { return nil },
			Execute: func(ctx context.Context, _ RunContext, _ provider.ToolCall) (json.RawMessage, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}},
		Timeout: 5 * time.Millisecond, MaxCallsPerRun: 2, MaxArgumentBytes: 16, MaxResultBytes: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := testRunContext()
	if _, err := gateway.Execute(context.Background(), run, provider.ToolCall{ID: "x", Name: "missing", Arguments: json.RawMessage(`{}`)}); !errors.Is(err, ErrDenied) {
		t.Fatalf("unknown tool error = %v", err)
	}
	if _, err := gateway.Execute(context.Background(), run, provider.ToolCall{ID: "x", Name: "slow", Arguments: json.RawMessage(`{"too":"large-value"}`)}); !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("oversized call error = %v", err)
	}
	if _, err := gateway.Execute(context.Background(), run, provider.ToolCall{ID: "x", Name: "slow", Arguments: json.RawMessage(`{}`)}); !errors.Is(err, ErrDenied) {
		t.Fatalf("denied call error = %v", err)
	}

	authorizer.err = nil
	gateway.Finish(run)
	if _, err := gateway.Execute(context.Background(), run, provider.ToolCall{ID: "y", Name: "slow", Arguments: json.RawMessage(`{}`)}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestGatewayDefinitionsAreStable(t *testing.T) {
	gateway, err := NewGateway(Config{
		Authorizer: &testAuthorizer{}, Store: &memoryResultStore{values: make(map[string]StoredResult)},
		Definitions: []Definition{
			testDefinition("zeta"),
			testDefinition("alpha"),
		},
		Timeout: time.Second, MaxCallsPerRun: 1, MaxArgumentBytes: 32, MaxResultBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	definitions := gateway.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "alpha" || definitions[1].Name != "zeta" {
		t.Fatalf("definitions = %+v", definitions)
	}
}

func testDefinition(name string) Definition {
	return Definition{
		Name: name, Description: name, Schema: json.RawMessage(`{"type":"object"}`), Capability: name, Scope: "run",
		Validate: func(json.RawMessage) error { return nil },
		Execute: func(context.Context, RunContext, provider.ToolCall) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}
}

func testRunContext() RunContext {
	return RunContext{AgentID: "agent", ComputerID: "computer", RunID: "run", Attempt: 1, Fence: 2, PlacementDesiredRevision: 3}
}

type testAuthorizer struct {
	calls int
	err   error
}

func (authorizer *testAuthorizer) Authorize(context.Context, RunContext, string, string) error {
	authorizer.calls++
	return authorizer.err
}

type memoryResultStore struct {
	mu     sync.Mutex
	values map[string]StoredResult
}

func (store *memoryResultStore) Load(_ context.Context, run RunContext, callID string) (StoredResult, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, found := store.values[resultKey(run, callID)]
	return value, found, nil
}

func (store *memoryResultStore) Save(_ context.Context, run RunContext, callID string, result StoredResult) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := resultKey(run, callID)
	if current, found := store.values[key]; found && (current.RequestHash != result.RequestHash || string(current.Result) != string(result.Result)) {
		return ErrIdempotencyConflict
	}
	result.Result = append(json.RawMessage(nil), result.Result...)
	store.values[key] = result
	return nil
}

func resultKey(run RunContext, callID string) string {
	digest := sha256.Sum256([]byte(run.RunID + callID))
	return string(digest[:])
}
