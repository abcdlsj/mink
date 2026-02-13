package agent

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
)

var agentNames = []string{
	"fox", "wolf", "hawk", "lion", "owl",
	"lynx", "crow", "bear", "puma", "eagle",
}

const maxActiveSubAgents = 2

func randAgentName() string {
	adj := []string{"swift", "brave", "calm", "fierce", "gentle", "noble", "proud", "sharp", "silent", "wild"}
	return adj[rand.Intn(len(adj))] + "-" + agentNames[rand.Intn(len(agentNames))]
}

type Supervisor struct {
	bus             *bus.Bus
	p               llm.Provider
	sm              *session.Manager
	hooks           *hook.Manager
	router          *command.Router
	toolGuard       command.Guard
	prompt          string
	cfg             config.Config
	agents          map[string]*Agent
	spawned         map[string]struct{}
	activeSubAgents int
	mu              sync.RWMutex
}

func NewSupervisor(b *bus.Bus, p llm.Provider, sm *session.Manager, h *hook.Manager, r *command.Router, prompt string) *Supervisor {
	s := &Supervisor{
		bus:     b,
		p:       p,
		sm:      sm,
		hooks:   h,
		router:  r,
		prompt:  prompt,
		agents:  make(map[string]*Agent),
		spawned: make(map[string]struct{}),
	}
	b.RegisterAgent(bus.AddrSystemSup, false)
	b.RegisterHandler(bus.TypeAgentSpawn, s.handleSpawn)
	b.RegisterHandler(bus.TypeDelegate, s.handleDelegate)
	return s
}

func (s *Supervisor) SetConfig(c config.Config) { s.cfg = c }
func (s *Supervisor) SetToolGuard(g command.Guard) {
	s.toolGuard = g
}

func (s *Supervisor) handleSpawn(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]any)
	if !ok {
		if p, ok := m.Payload.(map[string]string); ok {
			payload = map[string]any{"task": p["task"]}
		} else {
			return bus.Msg{}, fmt.Errorf("invalid spawn payload")
		}
	}

	task, _ := payload["task"].(string)
	shareCtx, _ := payload["share_context"].(bool)
	parentID := m.From

	if !s.acquireSpawnSlot() {
		return bus.Msg{}, fmt.Errorf("subagent limit reached: at most %d active subagents", maxActiveSubAgents)
	}

	child := s.SpawnWithContext(parentID, shareCtx)

	_ = s.bus.Pub(bus.Msg{
		Type: bus.TypeAgentSpawn,
		From: child.ID(),
		To:   bus.AddrBroadcast,
		Payload: map[string]string{
			"agent_id": child.ID(),
			"parent":   parentID,
			"task":     task,
		},
	})

	go func() {
		err := child.Run(ctx, bus.AddrBroadcast, task)
		result := s.extractLastResponse(child)
		if err != nil {
			result = fmt.Sprintf("error: %v", err)
		}
		_ = s.bus.Pub(bus.Msg{
			Type: bus.TypeAgentDone,
			From: child.ID(),
			To:   bus.AddrBroadcast,
			Payload: map[string]string{
				"agent_id": child.ID(),
				"result":   result,
			},
		})
		s.Kill(child.ID())
	}()

	return bus.Msg{
		Type: bus.TypeAgentSpawn,
		From: bus.AddrSystemSup,
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
		targetID = bus.AddrBroadcast
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

func (s *Supervisor) Spawn(parentID string) *Agent {
	return s.SpawnWithContext(parentID, false)
}

func (s *Supervisor) SpawnWithContext(parentID string, shareCtx bool) *Agent {
	id := bus.Agent("[agent]" + randAgentName())

	sess, _ := s.sm.Create()
	child := New(id, s.p, sess,
		WithBus(s.bus),
		WithHooks(s.hooks),
		WithRouter(s.router),
		WithToolGuard(s.toolGuard),
		WithPrompt(s.prompt),
		WithSubAgent(true),
		WithConfig(s.cfg),
	)

	s.bus.RegisterAgent(id, shareCtx)
	if shareCtx {
		ctx := s.bus.ForkContext(parentID, id)
		if conn, ok := s.bus.GetAgent(id); ok {
			conn.Context = ctx
		}
	}

	s.mu.Lock()
	s.agents[id] = child
	s.spawned[id] = struct{}{}
	s.mu.Unlock()

	return child
}

func (s *Supervisor) Kill(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.agents[id]; ok {
		delete(s.agents, id)
		if _, spawned := s.spawned[id]; spawned {
			delete(s.spawned, id)
			if s.activeSubAgents > 0 {
				s.activeSubAgents--
			}
		}
		s.bus.UnregisterAgent(id)
	}
}

func (s *Supervisor) Register(a *Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[a.ID()] = a
}

func (s *Supervisor) acquireSpawnSlot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeSubAgents >= maxActiveSubAgents {
		return false
	}
	s.activeSubAgents++
	return true
}

func (s *Supervisor) Get(id string) (*Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[id]
	return a, ok
}

func (s *Supervisor) extractLastResponse(a *Agent) string {
	msgs := a.Session().Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != "" {
			content := []rune(msgs[i].Content)
			if len(content) > 2000 {
				return string(content[:2000]) + "..."
			}
			return string(content)
		}
	}
	return "completed"
}
