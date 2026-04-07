package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/abcdlsj/mink/config"
)

type AgentDescriptor struct {
	ID            string                 `toml:"id"`
	Name          string                 `toml:"name"`
	Capabilities  []string               `toml:"capabilities"`
	Model         string                 `toml:"model"`
	SoulPath      string                 `toml:"soul_path"`
	Prompt        string                 `toml:"prompt"`
	Tools         []string               `toml:"tools"`
	MaxConcurrent int                    `toml:"max_concurrent"`
	Heartbeat     *config.HeartbeatConfig `toml:"heartbeat"`
}

type AgentStatus string

const (
	StatusIdle     AgentStatus = "idle"
	StatusBusy     AgentStatus = "busy"
	StatusSleeping AgentStatus = "sleeping"
	StatusOffline  AgentStatus = "offline"
)

type AgentState struct {
	Descriptor AgentDescriptor
	Status     AgentStatus
	ActiveRuns []string
	LastActive time.Time
	StartedAt  time.Time
}

type Registry struct {
	agents map[string]*AgentState
	mu     sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{agents: make(map[string]*AgentState)}
}

func (r *Registry) Register(desc AgentDescriptor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if desc.ID == "" {
		return fmt.Errorf("agent ID must not be empty")
	}
	if _, exists := r.agents[desc.ID]; exists {
		return fmt.Errorf("duplicate agent ID: %s", desc.ID)
	}
	if desc.MaxConcurrent <= 0 {
		desc.MaxConcurrent = 1
	}
	now := time.Now()
	r.agents[desc.ID] = &AgentState{
		Descriptor: desc,
		Status:     StatusIdle,
		StartedAt:  now,
		LastActive: now,
	}
	return nil
}

func (r *Registry) Get(id string) *AgentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[id]
}

func (r *Registry) All() []AgentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentState, 0, len(r.agents))
	for _, s := range r.agents {
		out = append(out, *s)
	}
	return out
}

func (r *Registry) SetStatus(id string, status AgentStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.agents[id]; ok {
		s.Status = status
		s.LastActive = time.Now()
	}
}

func (r *Registry) AddRun(id, runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.agents[id]
	if !ok {
		return
	}
	s.ActiveRuns = append(s.ActiveRuns, runID)
	s.Status = StatusBusy
	s.LastActive = time.Now()
}

func (r *Registry) RemoveRun(id, runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.agents[id]
	if !ok {
		return
	}
	for i, rid := range s.ActiveRuns {
		if rid == runID {
			s.ActiveRuns = append(s.ActiveRuns[:i], s.ActiveRuns[i+1:]...)
			break
		}
	}
	if len(s.ActiveRuns) == 0 {
		s.Status = StatusIdle
	}
	s.LastActive = time.Now()
}

func (r *Registry) FindByCapability(cap string) []AgentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []AgentState
	for _, s := range r.agents {
		if s.Status == StatusOffline {
			continue
		}
		for _, c := range s.Descriptor.Capabilities {
			if c == cap {
				out = append(out, *s)
				break
			}
		}
	}
	return out
}

func (r *Registry) Available() []AgentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []AgentState
	for _, s := range r.agents {
		if s.Status == StatusOffline {
			continue
		}
		if len(s.ActiveRuns) < s.Descriptor.MaxConcurrent {
			out = append(out, *s)
		}
	}
	return out
}

var ErrNoAvailableAgent = fmt.Errorf("no available agent for requested capabilities")

// Route: filter by caps → prefer idle → fewest active runs
func (r *Registry) Route(caps []string) (AgentState, error) {
	candidates := r.findCandidates(caps)
	if len(candidates) == 0 {
		return AgentState{}, ErrNoAvailableAgent
	}

	bestIdx := -1
	for i := range candidates {
		c := &candidates[i]
		if c.Status == StatusOffline {
			continue
		}
		if len(c.ActiveRuns) >= c.Descriptor.MaxConcurrent {
			continue
		}
		if bestIdx == -1 {
			bestIdx = i
			continue
		}
		best := &candidates[bestIdx]
		if statusPriority(c.Status) < statusPriority(best.Status) {
			bestIdx = i
			continue
		}
		if statusPriority(c.Status) == statusPriority(best.Status) && len(c.ActiveRuns) < len(best.ActiveRuns) {
			bestIdx = i
		}
	}
	if bestIdx == -1 {
		return AgentState{}, ErrNoAvailableAgent
	}
	return candidates[bestIdx], nil
}

func (r *Registry) findCandidates(caps []string) []AgentState {
	if len(caps) == 0 {
		return r.Available()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []AgentState
	for _, s := range r.agents {
		if s.Status == StatusOffline {
			continue
		}
		if hasAllCaps(s.Descriptor.Capabilities, caps) {
			out = append(out, *s)
		}
	}
	return out
}

func hasAllCaps(have, need []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, c := range have {
		set[c] = struct{}{}
	}
	for _, c := range need {
		if _, ok := set[c]; !ok {
			return false
		}
	}
	return true
}

func statusPriority(s AgentStatus) int {
	switch s {
	case StatusIdle:
		return 0
	case StatusSleeping:
		return 1
	case StatusBusy:
		return 2
	default:
		return 3
	}
}
