package agent

import (
	"sync"
	"time"
)

// AgentDescriptor is the persistent identity of an agent, loaded from config.
type AgentDescriptor struct {
	ID            string           `toml:"id"`
	Name          string           `toml:"name"`
	Capabilities  []string         `toml:"capabilities"`
	Model         string           `toml:"model"`
	SoulPath      string           `toml:"soul_path"`
	Prompt        string           `toml:"prompt"`
	Tools         []string         `toml:"tools"`
	MaxConcurrent int              `toml:"max_concurrent"`
	Heartbeat     *HeartbeatConfig `toml:"heartbeat"`
}

// HeartbeatConfig controls proactive agent action when idle.
type HeartbeatConfig struct {
	Schedule string `toml:"schedule"` // cron expression
	Prompt   string `toml:"prompt"`   // what to do on heartbeat
}

// AgentStatus represents the runtime status of an agent.
type AgentStatus string

const (
	StatusIdle     AgentStatus = "idle"
	StatusBusy     AgentStatus = "busy"
	StatusSleeping AgentStatus = "sleeping"
	StatusOffline  AgentStatus = "offline"
)

// AgentState is the runtime state of a registered agent.
type AgentState struct {
	Descriptor AgentDescriptor
	Status     AgentStatus
	ActiveRuns []string
	LastActive time.Time
	StartedAt  time.Time
}

// Registry tracks agent descriptors and runtime state.
type Registry struct {
	agents map[string]*AgentState
	mu     sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{agents: make(map[string]*AgentState)}
}

// Register adds or updates an agent descriptor and sets it to idle.
func (r *Registry) Register(desc AgentDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
}

// Get returns the state for an agent, or nil if not found.
func (r *Registry) Get(id string) *AgentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[id]
}

// All returns a snapshot of all agent states.
func (r *Registry) All() []AgentState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentState, 0, len(r.agents))
	for _, s := range r.agents {
		out = append(out, *s)
	}
	return out
}

// SetStatus updates an agent's status and touches LastActive.
func (r *Registry) SetStatus(id string, status AgentStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.agents[id]; ok {
		s.Status = status
		s.LastActive = time.Now()
	}
}

// AddRun records a new active run for an agent and sets status to busy.
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

// RemoveRun removes a run and sets status to idle if no runs remain.
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

// FindByCapability returns agents that declare the given capability and are not offline.
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

// Available returns agents that are idle or have capacity for more runs.
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
