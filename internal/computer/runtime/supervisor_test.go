package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/google/uuid"
)

func TestSupervisorKeepsIdleSlotsLightweightAndRunsAgentsIndependently(t *testing.T) {
	factory := &testFactory{started: make(chan string, 3), release: make(map[string]chan struct{})}
	supervisor, err := NewSupervisor(factory)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	root := t.TempDir()
	agents := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for _, agentID := range agents {
		if err := supervisor.Reconcile(testSlot(agentID, 1, root)); err != nil {
			t.Fatal(err)
		}
	}
	if slots, active := supervisor.Counts(); slots != 3 || active != 0 || factory.opens.Load() != 0 {
		t.Fatalf("idle counts = slots:%d active:%d opens:%d", slots, active, factory.opens.Load())
	}

	leases := make([]*Lease, 0, len(agents))
	for _, agentID := range agents {
		lease, err := supervisor.Acquire(context.Background(), agentID, 1)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
		go lease.Execute(Execution{RunID: uuid.NewString(), Attempt: 1, Fence: 1})
	}
	seen := make(map[string]bool)
	for range agents {
		select {
		case agentID := <-factory.started:
			seen[agentID] = true
		case <-time.After(time.Second):
			t.Fatal("agents did not start independently")
		}
	}
	if len(seen) != 3 {
		t.Fatalf("started agents = %v", seen)
	}
	if _, err := supervisor.Acquire(context.Background(), agents[0], 1); !errors.Is(err, ErrBusy) {
		t.Fatalf("same-agent second run error = %v", err)
	}
	close(factory.release[agents[0]])
	if err := leases[0].Close(); err != nil {
		t.Fatal(err)
	}
	if slots, active := supervisor.Counts(); slots != 3 || active != 2 {
		t.Fatalf("post-A counts = slots:%d active:%d", slots, active)
	}
	for index := 1; index < len(leases); index++ {
		close(factory.release[agents[index]])
		if err := leases[index].Close(); err != nil {
			t.Fatal(err)
		}
	}
	if slots, active := supervisor.Counts(); slots != 3 || active != 0 {
		t.Fatalf("finished counts = slots:%d active:%d", slots, active)
	}
}

func TestSupervisorReconcileCancelsOnlyChangedAgent(t *testing.T) {
	factory := &testFactory{started: make(chan string, 2), release: make(map[string]chan struct{})}
	supervisor, err := NewSupervisor(factory)
	if err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	root := t.TempDir()
	agentA := uuid.NewString()
	agentB := uuid.NewString()
	for _, agentID := range []string{agentA, agentB} {
		if err := supervisor.Reconcile(testSlot(agentID, 1, root)); err != nil {
			t.Fatal(err)
		}
	}
	leaseA, _ := supervisor.Acquire(context.Background(), agentA, 1)
	leaseB, _ := supervisor.Acquire(context.Background(), agentB, 1)
	resultA := make(chan error, 1)
	resultB := make(chan error, 1)
	go func() { _, err := leaseA.Execute(Execution{}); resultA <- err }()
	go func() { _, err := leaseB.Execute(Execution{}); resultB <- err }()
	<-factory.started
	<-factory.started
	if err := supervisor.Reconcile(testSlot(agentA, 2, root)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-resultA:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("changed agent error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("changed agent was not cancelled")
	}
	select {
	case err := <-resultB:
		t.Fatalf("unchanged agent stopped: %v", err)
	default:
	}
	close(factory.release[agentB])
	if err := leaseA.Close(); err != nil {
		t.Fatal(err)
	}
	if err := leaseB.Close(); err != nil {
		t.Fatal(err)
	}
}

type testFactory struct {
	mu      sync.Mutex
	started chan string
	release map[string]chan struct{}
	opens   atomic.Int64
}

func (*testFactory) Validate(SlotConfig) error { return nil }

func (factory *testFactory) Open(_ context.Context, config SlotConfig) (Engine, error) {
	factory.opens.Add(1)
	factory.mu.Lock()
	release := make(chan struct{})
	factory.release[config.AgentID] = release
	factory.mu.Unlock()
	return &testEngine{agentID: config.AgentID, started: factory.started, release: release}, nil
}

type testEngine struct {
	agentID string
	started chan string
	release <-chan struct{}
}

func (engine *testEngine) Execute(ctx context.Context, _ Execution) (Completion, error) {
	engine.started <- engine.agentID
	select {
	case <-ctx.Done():
		return Completion{}, context.Canceled
	case <-engine.release:
		return Completion{Outcome: OutcomeSucceeded, Body: "done"}, nil
	}
}

func (*testEngine) Close() error { return nil }

func testSlot(agentID string, revision uint64, root string) SlotConfig {
	base := filepath.Join(root, fmt.Sprintf("%s-%d", agentID, revision))
	return SlotConfig{
		AgentID: agentID, ComputerID: "computer-test", PlacementDesiredRevision: revision,
		AgentProfile: &agentv1.AgentProfile{AgentId: agentID, Revision: revision, DisplayName: agentID, Role: "worker", Mission: "test"},
		RuntimeSpec:  &agentv1.AgentRuntimeSpec{AgentId: agentID, Revision: revision, Engine: agentv1.EngineKind_ENGINE_KIND_BUILTIN},
		Workspace:    filepath.Join(base, "workspace"), Home: filepath.Join(base, "home"),
		Temp: filepath.Join(base, "temp"), Cache: filepath.Join(base, "cache"),
	}
}
