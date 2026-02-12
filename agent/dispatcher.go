package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
)

const workerIdleTTL = 5 * time.Minute

type workerState struct {
	q      chan bus.Msg
	cancel context.CancelFunc
}

type Dispatcher struct {
	bus         *bus.Bus
	sm          *session.Manager
	p           llm.Provider
	agentID     string
	hooks       *hook.Manager
	router      *command.Router
	prompt      string
	cfg         config.Config
	agents      map[string]*Agent
	workers     map[string]*workerState
	skillLoader *skill.Loader
	mu          sync.RWMutex
}

type usageSnapshot struct {
	Messages int
	Total    int
	Input    int
	Output   int
	System   int
	Tool     int
	Source   string
}

func NewDispatcher(b *bus.Bus, sm *session.Manager, p llm.Provider) *Dispatcher {
	d := &Dispatcher{
		bus:     b,
		sm:      sm,
		p:       p,
		agentID: bus.AddrAgentMain,
		hooks:   hook.NewManager(),
		agents:  make(map[string]*Agent),
		workers: make(map[string]*workerState),
	}
	return d
}

func (d *Dispatcher) SetAgentID(id string)           { d.agentID = id }
func (d *Dispatcher) SetHooks(h *hook.Manager)       { d.hooks = h }
func (d *Dispatcher) SetRouter(r *command.Router)    { d.router = r }
func (d *Dispatcher) SetPrompt(p string)             { d.prompt = p }
func (d *Dispatcher) SetConfig(c config.Config)      { d.cfg = c }
func (d *Dispatcher) SetSkillLoader(l *skill.Loader) { d.skillLoader = l }

func (d *Dispatcher) Handle(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	if m.To != bus.AddrBroadcast && m.To != d.agentID {
		return bus.Msg{}, nil
	}

	if m.Type == bus.TypeSessionReset {
		src, ok := m.Payload.(string)
		if !ok || src == "" {
			src = m.From
		}
		if src == "" {
			return d.inputError(m.From, "invalid session reset payload"), nil
		}
		d.resetAgent(src)
		return bus.Msg{}, nil
	}

	if m.Type == bus.TypeSessionCompact {
		src := m.From
		if src == "" {
			return d.inputError(m.From, "invalid source"), nil
		}
		note := ""
		if s, ok := m.Payload.(string); ok {
			note = s
		}
		a := d.getOrCreateAgent(src)
		out, err := a.Compact(ctx, src, note)
		if err != nil {
			return d.inputError(src, fmt.Sprintf("compact failed: %v", err)), nil
		}
		if out != "" {
			_ = d.bus.Pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    d.agentID,
				To:      src,
				Payload: out,
			})
		}
		return bus.Msg{}, nil
	}

	src := m.From
	if src == "" {
		return d.inputError(m.From, "invalid source"), nil
	}
	if _, ok := m.Payload.(string); !ok {
		return d.inputError(src, "input payload must be string"), nil
	}
	w := d.ensureWorker(ctx, src)

	select {
	case w.q <- m:
		return bus.Msg{}, nil
	default:
		return bus.Msg{
			Type:    bus.TypeAssistant,
			From:    d.agentID,
			Payload: "busy",
			To:      src,
		}, nil
	}
}

func (d *Dispatcher) resetAgent(src string) {
	var cancel context.CancelFunc

	d.mu.Lock()
	delete(d.agents, src)
	if w, ok := d.workers[src]; ok {
		cancel = w.cancel
		delete(d.workers, src)
	}
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if isTelegramSource(src) {
		_ = d.sm.Delete(src)
	}
}

func (d *Dispatcher) worker(ctx context.Context, src string, q chan bus.Msg) {
	idle := time.NewTimer(workerIdleTTL)
	defer idle.Stop()

	for {
		select {
		case m := <-q:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(workerIdleTTL)

			a := d.getOrCreateAgent(src)
			in, ok := m.Payload.(string)
			if !ok {
				_ = d.bus.Pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    d.agentID,
					Payload: "error: invalid input payload",
					To:      src,
				})
				continue
			}
			if err := a.Run(ctx, src, in); err != nil {
				_ = d.bus.Pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    d.agentID,
					Payload: fmt.Sprintf("error: %v", err),
					To:      src,
				})
			}
			_ = d.bus.Pub(bus.Msg{
				Type: bus.TypeTurnDone,
				From: d.agentID,
				To:   src,
			})
		case <-idle.C:
			d.removeWorker(src)
			return
		case <-ctx.Done():
			d.removeWorker(src)
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

	var sess *session.Session
	if isTelegramSource(src) {
		sess, _ = d.sm.GetOrCreate(src)
	} else {
		sess, _ = d.sm.Create()
	}
	a := New(d.agentID, d.p, sess,
		WithBus(d.bus),
		WithHooks(d.hooks),
		WithRouter(d.router),
		WithPrompt(d.prompt),
		WithConfig(d.cfg),
	)
	if d.skillLoader != nil {
		skill.RegisterTools(a.Tools(), d.skillLoader)
	}
	d.agents[src] = a
	return a
}

func isTelegramSource(src string) bool {
	return len(src) > 9 && src[:9] == "telegram:"
}

func (d *Dispatcher) Agent(src string) *Agent {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.agents[src]
}

func (d *Dispatcher) Usage(src string) (usageSnapshot, bool) {
	a := d.Agent(src)
	if a == nil {
		return usageSnapshot{}, false
	}
	u := a.TokenUsage()
	return usageSnapshot{
		Messages: u.Messages,
		Total:    u.Total,
		Input:    u.Input,
		Output:   u.Output,
		System:   u.System,
		Tool:     u.Tool,
		Source:   u.Source,
	}, true
}

func (d *Dispatcher) Start(ctx context.Context) {
	// 兼容广播输入
	ch := make(chan bus.Msg, 64)
	d.bus.Subscribe(bus.TypeUserInput, ch)

	go func() {
		for {
			select {
			case m := <-ch:
				d.Handle(ctx, m)
			case <-ctx.Done():
				return
			}
		}
	}()

	conn := d.bus.RegisterAgent(d.agentID, false)
	go func() {
		for {
			select {
			case m := <-conn.Send:
				if m.To != bus.AddrBroadcast && m.To != d.agentID {
					continue
				}
				switch m.Type {
				case bus.TypeUserInput, bus.TypeSessionReset, bus.TypeSessionCompact:
					d.Handle(ctx, m)
				case bus.TypeTaskDone:
					d.HandleTaskDone(ctx, m)
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
	src := payload["source"]

	var content string
	if status == "ok" {
		content = fmt.Sprintf("[Background task %s completed]\nOutput:\n%s", taskID, output)
	} else {
		content = fmt.Sprintf("[Background task %s failed]\nError: %s\nOutput:\n%s", taskID, errMsg, output)
	}

	if src == "" {
		d.mu.RLock()
		for s := range d.agents {
			src = s
			break
		}
		d.mu.RUnlock()
	}

	if src == "" {
		return
	}

	w := d.ensureWorker(ctx, src)

	select {
	case w.q <- bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      d.agentID,
		Payload: content,
	}:
	default:
		_ = d.bus.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    d.agentID,
			To:      src,
			Payload: "background result dropped: worker busy",
		})
	}
}

func (d *Dispatcher) inputError(to, message string) bus.Msg {
	if to == "" {
		to = bus.AddrBroadcast
	}
	return bus.Msg{
		Type:    bus.TypeAssistant,
		From:    d.agentID,
		To:      to,
		Payload: fmt.Sprintf("error: %s", message),
	}
}

func (d *Dispatcher) ensureWorker(parentCtx context.Context, src string) *workerState {
	d.mu.Lock()
	defer d.mu.Unlock()

	if w, ok := d.workers[src]; ok {
		return w
	}

	ctx, cancel := context.WithCancel(parentCtx)
	w := &workerState{
		q:      make(chan bus.Msg, 10),
		cancel: cancel,
	}
	d.workers[src] = w
	go d.worker(ctx, src, w.q)
	return w
}

func (d *Dispatcher) removeWorker(src string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.workers, src)
}
