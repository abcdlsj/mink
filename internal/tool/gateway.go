package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/internal/provider"
)

var (
	ErrDenied              = errors.New("tool call is not authorized")
	ErrInvalidCall         = errors.New("tool call is invalid")
	ErrBudgetExceeded      = errors.New("tool call budget exceeded")
	ErrIdempotencyConflict = errors.New("tool call idempotency conflict")
)

type RunContext struct {
	AgentID                  string
	ComputerID               string
	RunID                    string
	Attempt                  uint64
	Fence                    uint64
	PlacementDesiredRevision uint64
}

func (run RunContext) Valid() bool {
	return run.AgentID != "" && run.ComputerID != "" && run.RunID != "" && run.Attempt > 0 && run.Fence > 0 && run.PlacementDesiredRevision > 0
}

type Definition struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Capability  string
	Scope       string
	Validate    func(json.RawMessage) error
	Execute     func(context.Context, RunContext, provider.ToolCall) (json.RawMessage, error)
}

type Authorizer interface {
	Authorize(context.Context, RunContext, string, string) error
}

type StoredResult struct {
	RequestHash [sha256.Size]byte
	Result      json.RawMessage
}

type ResultStore interface {
	Load(context.Context, RunContext, string) (StoredResult, bool, error)
	Save(context.Context, RunContext, string, StoredResult) error
}

type Gateway struct {
	authorizer   Authorizer
	store        ResultStore
	tools        map[string]Definition
	timeout      time.Duration
	maxCalls     uint32
	maxResult    int
	maxArguments int

	mu    sync.Mutex
	calls map[string]uint32
}

type Config struct {
	Authorizer       Authorizer
	Store            ResultStore
	Definitions      []Definition
	Timeout          time.Duration
	MaxCallsPerRun   uint32
	MaxResultBytes   int
	MaxArgumentBytes int
}

func NewGateway(config Config) (*Gateway, error) {
	if config.Authorizer == nil || config.Store == nil || config.Timeout <= 0 || config.MaxCallsPerRun == 0 || config.MaxResultBytes <= 0 || config.MaxArgumentBytes <= 0 {
		return nil, errors.New("tool gateway security boundaries are required")
	}
	definitions := make(map[string]Definition, len(config.Definitions))
	for _, definition := range config.Definitions {
		if definition.Name == "" || definition.Description == "" || definition.Capability == "" || definition.Scope == "" ||
			len(definition.Schema) == 0 || !json.Valid(definition.Schema) || definition.Validate == nil || definition.Execute == nil {
			return nil, errors.New("tool definition is incomplete")
		}
		if _, exists := definitions[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate tool %q", definition.Name)
		}
		definitions[definition.Name] = definition
	}
	return &Gateway{
		authorizer: config.Authorizer, store: config.Store, tools: definitions, timeout: config.Timeout,
		maxCalls: config.MaxCallsPerRun, maxResult: config.MaxResultBytes, maxArguments: config.MaxArgumentBytes, calls: make(map[string]uint32),
	}, nil
}

func (gateway *Gateway) Definitions() []provider.Tool {
	definitions := make([]provider.Tool, 0, len(gateway.tools))
	for _, definition := range gateway.tools {
		definitions = append(definitions, provider.Tool{Name: definition.Name, Description: definition.Description, Schema: append(json.RawMessage(nil), definition.Schema...)})
	}
	sort.Slice(definitions, func(left, right int) bool { return definitions[left].Name < definitions[right].Name })
	return definitions
}

func (gateway *Gateway) Execute(ctx context.Context, run RunContext, call provider.ToolCall) (json.RawMessage, error) {
	if !run.Valid() || call.ID == "" || call.Name == "" || len(call.Arguments) == 0 || len(call.Arguments) > gateway.maxArguments || !json.Valid(call.Arguments) {
		return nil, ErrInvalidCall
	}
	definition, found := gateway.tools[call.Name]
	if !found {
		return nil, ErrDenied
	}
	if err := definition.Validate(call.Arguments); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCall, err)
	}
	hash := sha256.Sum256(append([]byte(call.Name+"\x00"), call.Arguments...))
	stored, found, err := gateway.store.Load(ctx, run, call.ID)
	if err != nil {
		return nil, fmt.Errorf("load tool result: %w", err)
	}
	if found {
		if stored.RequestHash != hash {
			return nil, ErrIdempotencyConflict
		}
		return append(json.RawMessage(nil), stored.Result...), nil
	}
	if err := gateway.consumeBudget(run); err != nil {
		return nil, err
	}
	if err := gateway.authorizer.Authorize(ctx, run, definition.Capability, definition.Scope); err != nil {
		return nil, ErrDenied
	}
	toolContext, cancel := context.WithTimeout(ctx, gateway.timeout)
	defer cancel()
	call.Arguments = append(json.RawMessage(nil), call.Arguments...)
	result, err := definition.Execute(toolContext, run, call)
	if err != nil {
		return nil, fmt.Errorf("execute tool: %w", err)
	}
	if len(result) == 0 || len(result) > gateway.maxResult || !json.Valid(result) {
		return nil, errors.New("tool result is invalid or exceeds its bound")
	}
	record := StoredResult{RequestHash: hash, Result: append(json.RawMessage(nil), result...)}
	if err := gateway.store.Save(ctx, run, call.ID, record); err != nil {
		return nil, fmt.Errorf("persist tool result: %w", err)
	}
	return append(json.RawMessage(nil), result...), nil
}

func (gateway *Gateway) Finish(run RunContext) {
	if !run.Valid() {
		return
	}
	gateway.mu.Lock()
	delete(gateway.calls, budgetKey(run))
	gateway.mu.Unlock()
}

func (gateway *Gateway) consumeBudget(run RunContext) error {
	key := budgetKey(run)
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.calls[key] >= gateway.maxCalls {
		return ErrBudgetExceeded
	}
	gateway.calls[key]++
	return nil
}

func budgetKey(run RunContext) string {
	return fmt.Sprintf("%s:%d:%d", run.RunID, run.Attempt, run.Fence)
}
