package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Core struct {
	id           string
	p            llm.Provider
	reg          *tool.Registry
	sm           *session.Manager
	bus          *bus.Bus
	hooks        *hook.Manager
	router       *cmd.Router
	customPrompt string
	tv           map[string]*toolView
	sessions     map[string]string
	mu           sync.RWMutex
	workers      map[string]chan bus.Msg
}

func New(id string, p llm.Provider, dir string, b *bus.Bus, h *hook.Manager, r *cmd.Router, customPrompt string) *Core {
	c := &Core{
		id:           id,
		p:            p,
		reg:          tool.NewRegistry(),
		sm:           session.NewManager(dir),
		bus:          b,
		hooks:        h,
		router:       r,
		customPrompt: customPrompt,
		tv:           make(map[string]*toolView),
		sessions:     make(map[string]string),
		workers:      make(map[string]chan bus.Msg),
	}
	c.bus.RegisterHandler(bus.TypeUserInput, c.Handle)
	return c
}

func (c *Core) ID() string                 { return c.id }
func (c *Core) Tools() *tool.Registry      { return c.reg }
func (c *Core) Sessions() *session.Manager { return c.sm }

func (c *Core) Handle(ctx context.Context, m bus.Msg) (bus.Msg, error) {
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
			Payload: "busy",
			To:      src,
		}, nil
	}
}

func (c *Core) worker(ctx context.Context, src string, q chan bus.Msg) {
	for {
		select {
		case m := <-q:
			in := m.Payload.(string)
			if err := c.Run(ctx, src, in); err != nil {
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

func (c *Core) Run(ctx context.Context, src, in string) error {
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
