package bus

import (
	"context"
	"fmt"
)

type Router struct {
	bus    *Bus
	routes map[string]RouteRule
}

type RouteRule struct {
	From    string
	Type    string
	To      string
	Handler Handler
}

func NewRouter(b *Bus) *Router {
	return &Router{
		bus:    b,
		routes: make(map[string]RouteRule),
	}
}

func (r *Router) AddRule(name string, rule RouteRule) {
	r.routes[name] = rule
}

func (r *Router) Route(ctx context.Context, m Msg) error {
	for _, rule := range r.routes {
		if !match(rule.From, m.From) {
			continue
		}
		if !match(rule.Type, m.Type) {
			continue
		}

		if rule.To != "" {
			m.To = rule.To
		}

		if rule.Handler != nil {
			resp, err := rule.Handler(ctx, m)
			if err != nil {
				return err
			}
			if resp.Type != "" {
				r.bus.Pub(resp)
			}
			return nil
		}
	}

	r.bus.Pub(m)
	return nil
}

func match(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	return pattern == value
}

type Coordinator struct {
	bus      *Bus
	agents   map[string]*SubAgent
	sessions map[string]*Session
}

type SubAgent struct {
	ID       string
	ParentID string
	Context  MsgContext
	Bus      *Bus
}

type Session struct {
	ID      string
	Agents  []string
	Context MsgContext
}

func NewCoordinator(b *Bus) *Coordinator {
	return &Coordinator{
		bus:      b,
		agents:   make(map[string]*SubAgent),
		sessions: make(map[string]*Session),
	}
}

func (c *Coordinator) Spawn(parentID string, shareCtx bool) (*SubAgent, error) {
	id := fmt.Sprintf("agent-%d", len(c.agents)+1)

	ctx := MsgContext{AgentID: id}
	if parentID != "" {
		ctx.ParentID = parentID
		if shareCtx {
			ctx.SessionID = c.sessions[parentID].Context.SessionID
		}
	}

	agent := &SubAgent{
		ID:       id,
		ParentID: parentID,
		Context:  ctx,
		Bus:      c.bus,
	}

	c.agents[id] = agent
	c.bus.RegisterAgent(id, shareCtx)

	return agent, nil
}

func (c *Coordinator) Kill(id string) {
	if agent, ok := c.agents[id]; ok {
		c.bus.UnregisterAgent(id)
		delete(c.agents, id)

		if agent.ParentID != "" {
			c.bus.Pub(Msg{
				Type: TypeAgentDie,
				From: id,
				To:   agent.ParentID,
				Payload: map[string]string{
					"agent_id": id,
					"summary":  "child agent completed",
				},
			})
		}
	}
}

func (c *Coordinator) ShareContext(fromID, toID string, data map[string]any) {
	if _, ok := c.agents[fromID]; ok {
		if to, ok := c.agents[toID]; ok {
			for k, v := range data {
				to.Context.Data[k] = v
			}

			c.bus.Pub(Msg{
				Type: TypeContextShare,
				From: fromID,
				To:   toID,
				Payload: map[string]any{
					"context": to.Context,
				},
			})
		}
	}
}

func (c *Coordinator) RunSubTask(parentID string, task string, shareCtx bool) (string, error) {
	child, err := c.Spawn(parentID, shareCtx)
	if err != nil {
		return "", err
	}

	ctx := context.Background()
	resp, err := c.bus.Req(ctx, Msg{
		Type: TypeUserInput,
		From: parentID,
		To:   child.ID,
		Payload: map[string]string{
			"task": task,
		},
	})

	if err != nil {
		c.Kill(child.ID)
		return "", err
	}

	go func() {
		<-child.Bus.agents[child.ID].Send
		c.Kill(child.ID)
	}()

	return resp.Payload.(string), nil
}
