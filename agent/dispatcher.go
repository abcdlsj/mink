package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
)

type Dispatcher struct {
	bus     *bus.Bus
	sm      *session.Manager
	p       llm.Provider
	hooks   *hook.Manager
	router  *cmd.Router
	prompt  string
	cfg     config.Config
	agents  map[string]*Agent
	workers map[string]chan bus.Msg
	mu      sync.RWMutex
}

func NewDispatcher(b *bus.Bus, sm *session.Manager, p llm.Provider, opts ...Option) *Dispatcher {
	d := &Dispatcher{
		bus:     b,
		sm:      sm,
		p:       p,
		hooks:   hook.NewManager(),
		agents:  make(map[string]*Agent),
		workers: make(map[string]chan bus.Msg),
	}
	for _, opt := range opts {
		opt((*Agent)(nil)) // dummy, we extract from opts differently
	}
	return d
}

func (d *Dispatcher) SetHooks(h *hook.Manager)   { d.hooks = h }
func (d *Dispatcher) SetRouter(r *cmd.Router)   { d.router = r }
func (d *Dispatcher) SetPrompt(p string)        { d.prompt = p }
func (d *Dispatcher) SetConfig(c config.Config) { d.cfg = c }

func (d *Dispatcher) Handle(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	if m.To != "*" && m.To != "main" {
		return bus.Msg{}, nil
	}

	src := m.From
	d.mu.Lock()
	q, ok := d.workers[src]
	if !ok {
		q = make(chan bus.Msg, 10)
		d.workers[src] = q
		go d.worker(ctx, src, q)
	}
	d.mu.Unlock()

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

func (d *Dispatcher) worker(ctx context.Context, src string, q chan bus.Msg) {
	for {
		select {
		case m := <-q:
			a := d.getOrCreateAgent(src)
			in := m.Payload.(string)
			if err := a.Run(ctx, src, in); err != nil {
				_ = d.bus.Pub(bus.Msg{
					Type:    bus.TypeAssistant,
					Payload: fmt.Sprintf("error: %v", err),
					To:      src,
				})
			}
			_ = d.bus.Pub(bus.Msg{
				Type: bus.TypeTurnDone,
				From: "main",
				To:   src,
			})
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) getOrCreateAgent(src string) *Agent {
	d.mu.RLock()
	if a, ok := d.agents[src]; ok {
		d.mu.RUnlock()
		return a
	}
	d.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if a, ok := d.agents[src]; ok {
		return a
	}

	sess, _ := d.sm.Create()
	a := New("main", d.p, sess,
		WithBus(d.bus),
		WithHooks(d.hooks),
		WithRouter(d.router),
		WithPrompt(d.prompt),
		WithConfig(d.cfg),
	)
	d.agents[src] = a
	return a
}

func (d *Dispatcher) Agent(src string) *Agent {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.agents[src]
}

func (d *Dispatcher) Start(ctx context.Context) {
	d.bus.RegisterHandler(bus.TypeUserInput, d.Handle)

	conn := d.bus.RegisterAgent("main", false)
	go func() {
		for {
			select {
			case m := <-conn.Send:
				if m.To != "*" && m.To != "main" {
					continue
				}
				switch m.Type {
				case bus.TypeTaskDone:
					d.HandleTaskDone(ctx, m)
				default:
					resp, _ := d.bus.Req(ctx, m)
					if resp.Type != "" {
						_ = d.bus.Pub(resp)
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (d *Dispatcher) HandleTaskDone(ctx context.Context, m bus.Msg) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return
	}

	taskID := payload["task_id"]
	output := payload["output"]
	status := payload["status"]
	errMsg := payload["error"]

	var content string
	if status == "ok" {
		content = fmt.Sprintf("[Background task %s completed]\nOutput:\n%s", taskID, output)
	} else {
		content = fmt.Sprintf("[Background task %s failed]\nError: %s\nOutput:\n%s", taskID, errMsg, output)
	}

	d.mu.RLock()
	var src string
	for s := range d.agents {
		src = s
		break
	}
	d.mu.RUnlock()

	if src == "" {
		return
	}

	d.mu.Lock()
	q, ok := d.workers[src]
	if !ok {
		q = make(chan bus.Msg, 10)
		d.workers[src] = q
		go d.worker(ctx, src, q)
	}
	d.mu.Unlock()

	q <- bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      "main",
		Payload: content,
	}
}
