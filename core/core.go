package core

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Core struct {
	id       string
	p        llm.Provider
	reg      *tool.Registry
	sm       *session.Manager
	bus      *bus.Bus
	tv       map[string]*toolView
	sessions map[string]string
	mu       sync.RWMutex
	workers  map[string]chan bus.Msg
}

func New(id string, p llm.Provider, dir string, b *bus.Bus) *Core {
	c := &Core{
		id:       id,
		p:        p,
		reg:      tool.NewRegistry(),
		sm:       session.NewManager(dir),
		bus:      b,
		tv:       make(map[string]*toolView),
		sessions: make(map[string]string),
		workers:  make(map[string]chan bus.Msg),
	}
	c.bus.RegisterHandler(bus.TypeUserInput, c.handleInput)
	return c
}

func (c *Core) handleInput(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	if m.To != "" && m.To != c.id && m.To != "*" {
		return bus.Msg{}, nil
	}

	src := m.From
	c.mu.Lock()
	q, ok := c.workers[src]
	if !ok {
		q = make(chan bus.Msg, 10)
		c.workers[src] = q
		go c.worker(ctx, src, q)
	}
	c.mu.Unlock()

	select {
	case q <- m:
		return bus.Msg{}, nil
	default:
		return bus.Msg{
			Type:    bus.TypeAssistant,
			Payload: "⏳ busy",
			To:      src,
		}, nil
	}
}

func (c *Core) worker(ctx context.Context, src string, q chan bus.Msg) {
	for {
		select {
		case m := <-q:
			in := m.Payload.(string)
			if err := c.run(ctx, src, in); err != nil {
				c.bus.Pub(bus.Msg{
					Type:    bus.TypeAssistant,
					Payload: fmt.Sprintf("error: %v", err),
					To:      src,
				})
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Core) Start(ctx context.Context) {
	conn := c.bus.RegisterAgent(c.id, false)
	ch := make(chan bus.Msg, 64)
	c.bus.Subscribe(bus.TypeUserInput, ch)

	go func() {
		for {
			select {
			case m := <-ch:
				if m.To != "" && m.To != "*" {
					continue
				}
				resp, _ := c.bus.Req(ctx, m)
				if resp.Type != "" {
					c.bus.Pub(resp)
				}
			case m := <-conn.Send:
				resp, _ := c.bus.Req(ctx, m)
				if resp.Type != "" {
					c.bus.Pub(resp)
				}
			case <-ctx.Done():
				c.bus.Unsubscribe(bus.TypeUserInput, ch)
				return
			}
		}
	}()
}

func (c *Core) session(src string) (string, error) {
	c.mu.RLock()
	if sid, ok := c.sessions[src]; ok {
		c.mu.RUnlock()
		return sid, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if sid, ok := c.sessions[src]; ok {
		return sid, nil
	}

	s, err := c.sm.Create()
	if err != nil {
		return "", err
	}

	c.sessions[src] = s.ID
	c.tv[src] = newToolView()
	return s.ID, nil
}

func (c *Core) run(ctx context.Context, src, in string) error {
	sid, err := c.session(src)
	if err != nil {
		return err
	}

	c.sm.AddMessage(sid, session.Message{Role: "user", Content: in})

	for i := 0; i < 100; i++ {
		done, err := c.step(ctx, src, sid)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}

	return fmt.Errorf("max steps")
}

func (c *Core) step(ctx context.Context, src, sid string) (bool, error) {
	c.mu.RLock()
	tv := c.tv[src]
	c.mu.RUnlock()

	h, _ := c.sm.GetHistory(sid, -1)
	msgs := c.buildMsgs(h)
	tools := tv.tools(c.reg)

	r, err := c.p.Chat(ctx, msgs, tools)
	if err != nil {
		return false, err
	}

	if len(r.ToolCalls) > 0 || r.Content != "" {
		var tcs []session.ToolCall
		for _, tc := range r.ToolCalls {
			tcs = append(tcs, session.ToolCall{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
		c.sm.AddMessage(sid, session.Message{
			Role:      "assistant",
			Content:   r.Content,
			ToolCalls: tcs,
		})
	}

	if r.Content != "" {
		c.bus.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    c.id,
			To:      src,
			Payload: r.Content,
		})
	}

	if len(r.ToolCalls) == 0 {
		return true, nil
	}

	for _, tc := range r.ToolCalls {
		tv.expand(tc.Name)
		out, err := c.execTool(ctx, tc)

		tr := session.ToolResult{ToolCallID: tc.ID, Content: out}
		if err != nil {
			tr.Error = err.Error()
		}
		c.sm.AddMessage(sid, session.Message{Role: "tool", ToolResults: []session.ToolResult{tr}})
	}

	return false, nil
}

func (c *Core) buildMsgs(h []session.Message) []llm.Message {
	var r []llm.Message
	r = append(r, llm.Message{Role: "system", Content: c.prompt()})

	for _, m := range h {
		msg := llm.Message{Role: m.Role, Content: m.Content}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args})
		}
		for _, tr := range m.ToolResults {
			msg.ToolResults = append(msg.ToolResults, llm.ToolResult{ToolCallID: tr.ToolCallID, Content: tr.Content, Error: tr.Error})
		}
		r = append(r, msg)
	}
	return r
}

func (c *Core) prompt() string {
	var b strings.Builder
	b.WriteString("You are a helpful coding assistant.\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range c.reg.All() {
		b.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Desc()))
	}
	return b.String()
}

type toolView struct {
	expanded map[string]bool
}

func newToolView() *toolView {
	return &toolView{expanded: make(map[string]bool)}
}

func (v *toolView) expand(name string) {
	v.expanded[name] = true
}

func (v *toolView) tools(reg *tool.Registry) []llm.Tool {
	var r []llm.Tool
	for _, t := range reg.All() {
		if v.expanded[t.Name()] {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  t.Schema(),
				},
			})
		} else {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  map[string]any{"type": "object"},
				},
			})
		}
	}
	return r
}

func (c *Core) execTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	if t := c.reg.Get(tc.Name); t != nil {
		return t.Run(ctx, tc.Args)
	}
	return "", fmt.Errorf("unknown: %s", tc.Name)
}
