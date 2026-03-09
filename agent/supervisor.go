package agent

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
)

var agentNames = []string{
	"fox", "wolf", "hawk", "lion", "owl",
	"lynx", "crow", "bear", "puma", "eagle",
}

const maxActiveSubAgents = 3

func randAgentName() string {
	adj := []string{"swift", "brave", "calm", "fierce", "gentle", "noble", "proud", "sharp", "silent", "wild"}
	return adj[rand.Intn(len(adj))] + "-" + agentNames[rand.Intn(len(agentNames))]
}

type Supervisor struct {
	deps            AgentDeps
	sm              *session.Manager
	agents          map[string]*Agent
	spawned         map[string]struct{}
	activeSubAgents int
	started         bool
	mu              sync.RWMutex
}

func NewSupervisor(deps AgentDeps, sm *session.Manager) *Supervisor {
	return &Supervisor{
		deps:    deps,
		sm:      sm,
		agents:  make(map[string]*Agent),
		spawned: make(map[string]struct{}),
	}
}

func (s *Supervisor) Start(_ context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()

	if s.deps.Bus != nil {
		s.deps.Bus.RegisterHandler(bus.TypeSubtaskRun, s.handleSubtaskRun)
	}
	return nil
}

func (s *Supervisor) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	ids := make([]string, 0, len(s.spawned))
	for id := range s.spawned {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	for _, id := range ids {
		s.kill(id)
	}

	if s.deps.Bus != nil {
		s.deps.Bus.UnregisterHandler(bus.TypeSubtaskRun)
	}
	return nil
}

func (s *Supervisor) handleSubtaskRun(ctx context.Context, m bus.Msg) (bus.Msg, error) {
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
	directOutput, _ := payload["direct_output"].(bool)
	parentID := m.From
	source := bus.SourceFrom(ctx)
	if task == "" {
		return bus.Msg{}, fmt.Errorf("task is required")
	}

	if !s.acquireSpawnSlot() {
		return bus.Msg{}, fmt.Errorf("subagent limit reached: at most %d active subagents", maxActiveSubAgents)
	}

	child := s.createChild(parentID, source, shareCtx)
	directStr := "false"
	if directOutput {
		directStr = "true"
	}
	_ = s.deps.Bus.Pub(bus.Msg{
		Type: bus.TypeAgentSpawn,
		From: child.ID(),
		To:   bus.AddrBroadcast,
		Payload: map[string]string{
			"agent_id":      child.ID(),
			"parent":        parentID,
			"task":          task,
			"direct_output": directStr,
		},
	})

	err := child.Run(ctx, bus.AddrBroadcast, task)
	result := s.extractLastResponse(child)
	if err != nil {
		result = fmt.Sprintf("error: %v", err)
	}
	_ = s.deps.Bus.Pub(bus.Msg{
		Type: bus.TypeAgentDone,
		From: child.ID(),
		To:   bus.AddrBroadcast,
		Payload: map[string]string{
			"agent_id": child.ID(),
			"result":   result,
		},
	})
	s.kill(child.ID())
	if err != nil {
		return bus.Msg{}, err
	}

	return bus.Msg{
		Type: bus.TypeSubtaskRun,
		From: bus.AddrSystemSup,
		To:   parentID,
		Payload: map[string]string{
			"agent_id": child.ID(),
			"status":   "completed",
			"result":   result,
		},
	}, nil
}
func (s *Supervisor) createChild(parentID, source string, shareCtx bool) *Agent {
	id := bus.Agent("[agent]" + randAgentName())

	var sess *session.Session
	if shareCtx {
		if parent := s.parentSession(parentID, source); parent != nil {
			sess, _ = s.sm.Fork(parent)
		}
	}
	if sess == nil {
		sess, _ = s.sm.Create()
	}
	child := s.deps.newAgent(id, sess, true)

	s.deps.Bus.RegisterAgent(id)

	s.mu.Lock()
	s.agents[id] = child
	s.spawned[id] = struct{}{}
	s.mu.Unlock()

	return child
}

func (s *Supervisor) parentSession(parentID, source string) *session.Session {
	s.mu.RLock()
	if parent, ok := s.agents[parentID]; ok {
		s.mu.RUnlock()
		return parent.Session()
	}
	s.mu.RUnlock()
	if parentID != bus.AddrAgentMain || source == "" {
		return nil
	}
	parent, ok := s.sm.LookupSource(source)
	if !ok {
		return nil
	}
	return parent
}

func (s *Supervisor) kill(id string) {
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
		s.deps.Bus.UnregisterAgent(id)
	}
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
