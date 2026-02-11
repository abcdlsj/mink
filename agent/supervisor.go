package agent

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
)

var agentNames = []string{
	"fox", "wolf", "hawk", "lion", "owl",
	"lynx", "crow", "bear", "puma", "eagle",
}

func randAgentName() string {
	adj := []string{"swift", "brave", "calm", "fierce", "gentle", "noble", "proud", "sharp", "silent", "wild"}
	return adj[rand.Intn(len(adj))] + "-" + agentNames[rand.Intn(len(agentNames))]
}

type Supervisor struct {
	bus          *bus.Bus
	provider     llm.Provider
	dir          string
	hooks        *hook.Manager
	router       *cmd.Router
	customPrompt string
	agents       map[string]*Core
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
	payload, ok := m.Payload.(map[string]any)
	if !ok {
		// 兼容旧格式
		if p, ok := m.Payload.(map[string]string); ok {
			payload = map[string]any{"task": p["task"]}
		} else {
			return bus.Msg{}, fmt.Errorf("invalid spawn payload")
		}
	}

	task, _ := payload["task"].(string)
	shareCtx, _ := payload["share_context"].(bool)
	parentID := m.From

	child := s.SpawnWithContext(parentID, shareCtx)

	// 广播 agent 创建事件
	s.bus.Pub(bus.Msg{
		Type: bus.TypeAgentSpawn,
		From: child.ID(),
		To:   "*",
		Payload: map[string]string{
			"agent_id": child.ID(),
			"parent":   parentID,
			"task":     task,
		},
	})

	go func() {
		// 子 agent 输出广播到 "*"，让所有平台都能看到协作过程
		err := child.Run(ctx, "*", task)
		result := s.extractLastResponse(child)
		if err != nil {
			result = fmt.Sprintf("error: %v", err)
		}
		s.bus.Pub(bus.Msg{
			Type: bus.TypeAgentDone,
			From: child.ID(),
			To:   "*",
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
	return s.SpawnWithContext(parentID, false)
}

func (s *Supervisor) SpawnWithContext(parentID string, shareCtx bool) *Core {
	id := "[agent]" + randAgentName()
	child := New(id, s.provider, s.dir, s.bus, s.hooks, s.router, s.customPrompt)

	// 注册到 bus 并处理上下文共享
	s.bus.RegisterAgent(id, shareCtx)
	if shareCtx {
		ctx := s.bus.ForkContext(parentID, id)
		if conn, ok := s.bus.GetAgent(id); ok {
			conn.Context = ctx
		}
	}

	s.mu.Lock()
	s.agents[id] = child
	s.mu.Unlock()

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

func (s *Supervisor) extractLastResponse(c *Core) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, sid := range c.sessions {
		if h, err := c.sm.GetHistory(sid, -1); err == nil {
			for i := len(h) - 1; i >= 0; i-- {
				if h[i].Role == "assistant" && h[i].Content != "" {
					content := h[i].Content
					if len(content) > 2000 {
						content = content[:2000] + "..."
					}
					return content
				}
			}
		}
	}
	return "completed"
}
