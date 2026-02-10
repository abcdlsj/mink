package bus

import (
	"context"
	"fmt"
)

// Router 消息路由器
type Router struct {
	bus    *Bus
	routes map[string]RouteRule
}

// RouteRule 路由规则
type RouteRule struct {
	From    string   // 来源匹配
	Type    string   // 类型匹配
	To      string   // 目标
	Handler Handler  // 处理器
}

func NewRouter(b *Bus) *Router {
	return &Router{
		bus:    b,
		routes: make(map[string]RouteRule),
	}
}

// AddRule 添加路由规则
func (r *Router) AddRule(name string, rule RouteRule) {
	r.routes[name] = rule
}

// Route 路由消息
func (r *Router) Route(ctx context.Context, m Msg) error {
	// 匹配规则
	for _, rule := range r.routes {
		if !match(rule.From, m.From) {
			continue
		}
		if !match(rule.Type, m.Type) {
			continue
		}
		
		// 修改目标
		if rule.To != "" {
			m.To = rule.To
		}
		
		// 执行处理器
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
	
	// 默认广播
	r.bus.Pub(m)
	return nil
}

func match(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	return pattern == value
}

// Coordinator Agent协调器
type Coordinator struct {
	bus      *Bus
	agents   map[string]*SubAgent
	sessions map[string]*Session
}

// SubAgent 子Agent
type SubAgent struct {
	ID       string
	ParentID string
	Context  MsgContext
	Bus      *Bus
}

// Session 会话
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

// Spawn 创建子Agent
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

// Kill 终止Agent
func (c *Coordinator) Kill(id string) {
	if agent, ok := c.agents[id]; ok {
		c.bus.UnregisterAgent(id)
		delete(c.agents, id)
		
		// 通知父Agent
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

// ShareContext 共享上下文给子Agent
func (c *Coordinator) ShareContext(fromID, toID string, data map[string]any) {
	if from, ok := c.agents[fromID]; ok {
		if to, ok := c.agents[toID]; ok {
			// 合并数据
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

// RunSubTask 运行子任务
func (c *Coordinator) RunSubTask(parentID string, task string, shareCtx bool) (string, error) {
	child, err := c.Spawn(parentID, shareCtx)
	if err != nil {
		return "", err
	}
	
	// 发送任务
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
	
	// 等待完成（异步）
	go func() {
		// 监听子Agent完成
		<-child.Bus.agents[child.ID].Send
		c.Kill(child.ID)
	}()
	
	return resp.Payload.(string), nil
}
