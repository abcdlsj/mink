package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
)

type Supervisor struct {
	bus          *bus.Bus
	provider     llm.Provider
	dir          string
	hooks        *hook.Manager
	router       *cmd.Router
	customPrompt string
	agents       map[string]*Core
	counter      atomic.Int64
	mu           sync.RWMutex
}

func NewSupervisor(b *bus.Bus, p llm.Provider, dir string, h *hook.Manager, r *cmd.Router, customPrompt string) *Supervisor {
	s := &Supervisor{
		bus:          b,
		provider:     p,
		dir:          dir,
		hooks:        h,
		router:       r,
		customPrompt: customPrompt,
		agents:       make(map[string]*Core),
	}
	b.RegisterHandler(bus.TypeAgentSpawn, s.handleSpawn)
	b.RegisterHandler(bus.TypeDelegate, s.handleDelegate)
	return s
}

func (s *Supervisor) handleSpawn(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return bus.Msg{}, fmt.Errorf("invalid spawn payload")
	}

	task := payload["task"]
	parentID := m.From

	child := s.Spawn(parentID)
	go func() {
		err := child.Run(ctx, parentID, task)
		result := "completed"
		if err != nil {
			result = fmt.Sprintf("error: %v", err)
		}
		s.bus.Pub(bus.Msg{
			Type: bus.TypeAgentDone,
			From: child.ID(),
			To:   parentID,
			Payload: map[string]string{
				"agent_id": child.ID(),
				"result":   result,
			},
		})
		s.Kill(child.ID())
	}()

	return bus.Msg{
		Type: bus.TypeAgentSpawn,
		From: "supervisor",
		To:   parentID,
		Payload: map[string]string{
			"agent_id": child.ID(),
			"status":   "spawned",
		},
	}, nil
}

func (s *Supervisor) handleDelegate(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return bus.Msg{}, fmt.Errorf("invalid delegate payload")
	}

	task := payload["task"]
	targetID := payload["to"]
	if targetID == "" {
		targetID = "*"
	}

	s.mu.RLock()
	target, ok := s.agents[targetID]
	s.mu.RUnlock()

	if !ok {
		return bus.Msg{}, fmt.Errorf("agent not found: %s", targetID)
	}

	resultCh := make(chan string, 1)
	go func() {
		err := target.Run(ctx, m.From, task)
		if err != nil {
			resultCh <- fmt.Sprintf("error: %v", err)
		} else {
			resultCh <- "completed"
		}
	}()

	select {
	case result := <-resultCh:
		return bus.Msg{
			Type: bus.TypeReport,
			From: targetID,
			To:   m.From,
			Payload: map[string]string{
				"result": result,
			},
		}, nil
	case <-ctx.Done():
		return bus.Msg{}, ctx.Err()
	}
}

func (s *Supervisor) Spawn(parentID string) *Core {
	id := fmt.Sprintf("agent-%d", s.counter.Add(1))
	child := New(id, s.provider, s.dir, s.bus, s.hooks, s.router, s.customPrompt)

	s.mu.Lock()
	s.agents[id] = child
	s.mu.Unlock()

	s.bus.Pub(bus.Msg{
		Type: bus.TypeAgentSpawn,
		From: "supervisor",
		To:   parentID,
		Payload: map[string]string{
			"agent_id": id,
			"parent":   parentID,
		},
	})

	return child
}

func (s *Supervisor) Kill(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.agents[id]; ok {
		delete(s.agents, id)
		s.bus.UnregisterAgent(id)
	}
}

func (s *Supervisor) Register(a *Core) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[a.ID()] = a
}

func (s *Supervisor) Get(id string) (*Core, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[id]
	return a, ok
}
