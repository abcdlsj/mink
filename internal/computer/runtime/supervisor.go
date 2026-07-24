package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	placementfailure "github.com/abcdlsj/sumi/internal/placement/failure"
	"google.golang.org/protobuf/proto"
)

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

type Usage struct {
	InputUnits  uint64
	OutputUnits uint64
}

type ContextMessage struct {
	ID             string
	TargetSequence uint64
	AuthorKind     string
	AuthorID       string
	Body           string
}

type Execution struct {
	AgentID                  string
	ComputerID               string
	RunID                    string
	Attempt                  uint64
	Fence                    uint64
	PlacementDesiredRevision uint64
	Workspace                string
	Home                     string
	Temp                     string
	Cache                    string
	SpaceID                  string
	ThreadRootMessageID      string
	BasisTargetSequence      uint64
	Messages                 []ContextMessage
	CurrentInput             string
}

type Completion struct {
	Outcome           Outcome
	ErrorCode         string
	Body              string
	MentionedAgentIDs []string
	Usage             Usage
}

type Engine interface {
	Execute(context.Context, Execution) (Completion, error)
	Close() error
}

type SlotConfig struct {
	AgentID                  string
	ComputerID               string
	PlacementDesiredRevision uint64
	AgentProfile             *agentv1.AgentProfile
	RuntimeSpec              *agentv1.AgentRuntimeSpec
	Workspace                string
	Home                     string
	Temp                     string
	Cache                    string
}

type Factory interface {
	Validate(SlotConfig) error
	Open(context.Context, SlotConfig) (Engine, error)
}

var (
	ErrInvalidSlot = errors.New("runtime slot is invalid")
	ErrNotReady    = errors.New("runtime slot is not ready")
	ErrBusy        = errors.New("runtime slot already has an active run")
)

func ErrorCode(err error) string {
	if errors.Is(err, ErrInvalidSlot) {
		return placementfailure.RuntimeSpecInvalid
	}
	return placementfailure.EngineUnavailable
}

type slot struct {
	config SlotConfig
	active bool
	cancel context.CancelCauseFunc
}

type Supervisor struct {
	factory Factory
	mu      sync.Mutex
	slots   map[string]*slot
}

func NewSupervisor(factory Factory) (*Supervisor, error) {
	if factory == nil {
		return nil, errors.New("runtime factory is required")
	}
	return &Supervisor{factory: factory, slots: make(map[string]*slot)}, nil
}

func (s *Supervisor) Reconcile(config SlotConfig) error {
	config = cloneConfig(config)
	if err := validateSlotConfig(config); err != nil {
		return err
	}
	if err := s.factory.Validate(config); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, found := s.slots[config.AgentID]
	if found && sameConfig(current.config, config) {
		return nil
	}
	if found && current.cancel != nil {
		current.cancel(errors.New("runtime placement changed"))
	}
	s.slots[config.AgentID] = &slot{config: config}
	return nil
}

func (s *Supervisor) RemoveExcept(ready map[string]uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for agentID, current := range s.slots {
		revision, found := ready[agentID]
		if found && revision == current.config.PlacementDesiredRevision {
			continue
		}
		if current.cancel != nil {
			current.cancel(errors.New("runtime placement removed"))
		}
		delete(s.slots, agentID)
	}
}

func (s *Supervisor) Stop(agentID string, desiredRevision uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, found := s.slots[agentID]
	if !found || current.config.PlacementDesiredRevision != desiredRevision {
		return
	}
	if current.cancel != nil {
		current.cancel(errors.New("runtime slot stopped"))
	}
	delete(s.slots, agentID)
}

func (s *Supervisor) Acquire(parent context.Context, agentID string, desiredRevision uint64) (*Lease, error) {
	s.mu.Lock()
	current, found := s.slots[agentID]
	if !found || current.config.PlacementDesiredRevision != desiredRevision {
		s.mu.Unlock()
		return nil, ErrNotReady
	}
	if current.active {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	ctx, cancel := context.WithCancelCause(parent)
	current.active = true
	current.cancel = cancel
	config := cloneConfig(current.config)
	s.mu.Unlock()

	engine, err := s.factory.Open(ctx, config)
	if err != nil {
		cancel(err)
		s.release(agentID, desiredRevision, nil)
		return nil, fmt.Errorf("open runtime engine: %w", err)
	}
	return &Lease{supervisor: s, agentID: agentID, desiredRevision: desiredRevision, ctx: ctx, cancel: cancel, engine: engine, config: config}, nil
}

func (s *Supervisor) Counts() (slots, active int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, current := range s.slots {
		slots++
		if current.active {
			active++
		}
	}
	return slots, active
}

func (s *Supervisor) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for agentID, current := range s.slots {
		if current.cancel != nil {
			current.cancel(errors.New("runtime supervisor closed"))
		}
		delete(s.slots, agentID)
	}
	return nil
}

func (s *Supervisor) release(agentID string, desiredRevision uint64, engine Engine) error {
	var closeErr error
	if engine != nil {
		closeErr = engine.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, found := s.slots[agentID]
	if found && current.config.PlacementDesiredRevision == desiredRevision {
		current.active = false
		current.cancel = nil
	}
	return closeErr
}

type Lease struct {
	supervisor      *Supervisor
	agentID         string
	desiredRevision uint64
	ctx             context.Context
	cancel          context.CancelCauseFunc
	engine          Engine
	config          SlotConfig
	once            sync.Once
	err             error
}

func (l *Lease) Context() context.Context { return l.ctx }

func (l *Lease) Execute(execution Execution) (Completion, error) {
	execution.AgentID = l.config.AgentID
	execution.PlacementDesiredRevision = l.config.PlacementDesiredRevision
	execution.Workspace = l.config.Workspace
	execution.Home = l.config.Home
	execution.Temp = l.config.Temp
	execution.Cache = l.config.Cache
	return l.engine.Execute(l.ctx, execution)
}

func (l *Lease) Close() error {
	l.once.Do(func() {
		l.cancel(errors.New("runtime lease closed"))
		l.err = l.supervisor.release(l.agentID, l.desiredRevision, l.engine)
	})
	return l.err
}

func validateSlotConfig(config SlotConfig) error {
	if config.AgentID == "" || config.ComputerID == "" || config.PlacementDesiredRevision == 0 || config.AgentProfile == nil ||
		config.AgentProfile.GetAgentId() != config.AgentID || config.AgentProfile.GetRevision() == 0 || config.RuntimeSpec == nil ||
		config.RuntimeSpec.GetAgentId() != config.AgentID || config.RuntimeSpec.GetRevision() == 0 ||
		config.Workspace == "" || config.Home == "" || config.Temp == "" || config.Cache == "" {
		return ErrInvalidSlot
	}
	return nil
}

func cloneConfig(config SlotConfig) SlotConfig {
	if config.AgentProfile != nil {
		config.AgentProfile = proto.Clone(config.AgentProfile).(*agentv1.AgentProfile)
	}
	if config.RuntimeSpec != nil {
		config.RuntimeSpec = proto.Clone(config.RuntimeSpec).(*agentv1.AgentRuntimeSpec)
	}
	return config
}

func sameConfig(left, right SlotConfig) bool {
	return left.AgentID == right.AgentID && left.ComputerID == right.ComputerID && left.PlacementDesiredRevision == right.PlacementDesiredRevision &&
		left.Workspace == right.Workspace && left.Home == right.Home && left.Temp == right.Temp && left.Cache == right.Cache &&
		proto.Equal(left.AgentProfile, right.AgentProfile) && proto.Equal(left.RuntimeSpec, right.RuntimeSpec)
}
