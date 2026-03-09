package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
	"github.com/abcdlsj/mink/tool"
)

const workerIdleTTL = 5 * time.Minute

type workerState struct {
	q      chan bus.Msg
	cancel context.CancelFunc
}

type Dispatcher struct {
	deps           AgentDeps
	sm             *session.Manager
	agentID        string
	agents         map[string]*Agent
	workers        map[string]*workerState
	skillLoader    *skill.Loader
	resumeSessions map[string]string
	mu             sync.RWMutex
}

func NewDispatcher(deps AgentDeps, sm *session.Manager, sl *skill.Loader) *Dispatcher {
	return &Dispatcher{
		deps:        deps,
		sm:          sm,
		agentID:     bus.AddrAgentMain,
		agents:      make(map[string]*Agent),
		workers:     make(map[string]*workerState),
		skillLoader: sl,
	}
}

func (d *Dispatcher) SetConfig(c config.Config) {
	d.mu.Lock()
	d.deps.Config = c
	d.mu.Unlock()
}

func (d *Dispatcher) SetLLM(p llm.Provider, sel *llm.Sel) {
	d.mu.Lock()
	d.deps.Provider = p
	d.deps.Sel = sel
	d.mu.Unlock()
}

func (d *Dispatcher) ActiveSessions() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	m := make(map[string]string, len(d.agents))
	for src, a := range d.agents {
		m[src] = a.Session().ID()
	}
	return m
}

func (d *Dispatcher) SetResumeSessions(m map[string]string) {
	d.mu.Lock()
	d.resumeSessions = m
	d.mu.Unlock()
}

func (d *Dispatcher) SetSelfUpdateTool(t tool.Tool) {
	d.mu.Lock()
	d.deps.SelfUpdateTool = t
	d.mu.Unlock()
}

func (d *Dispatcher) ResetAgents() {
	d.mu.Lock()
	d.agents = make(map[string]*Agent)
	d.mu.Unlock()
}

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
			_ = d.deps.Bus.Pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    d.agentID,
				To:      src,
				Payload: out,
			})
		}
		return bus.Msg{}, nil
	}

	if m.Type == bus.TypeInterrupt {
		d.mu.RLock()
		if m.From != "" {
			if a, ok := d.agents[m.From]; ok {
				a.Interrupt()
			}
		} else {
			for _, a := range d.agents {
				a.Interrupt()
			}
		}
		d.mu.RUnlock()
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
				_ = d.deps.Bus.Pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    d.agentID,
					Payload: "error: invalid input payload",
					To:      src,
				})
				continue
			}
			var err error
			if m.Type == bus.TypeTaskDone {
				err = a.RunSystem(ctx, src, in)
			} else {
				err = a.Run(ctx, src, in)
			}
			if err != nil {
				_ = d.deps.Bus.Pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    d.agentID,
					Payload: fmt.Sprintf("error: %v", err),
					To:      src,
				})
			}
			_ = d.deps.Bus.Pub(bus.Msg{
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
	if sid, ok := d.resumeSessions[src]; ok {
		sess, _ = d.sm.Get(sid)
		delete(d.resumeSessions, src)
	}
	if sess == nil && isTelegramSource(src) {
		sess, _ = d.sm.GetOrCreate(src)
	}
	if sess == nil {
		sess, _ = d.sm.Create()
	}
	a := d.deps.newAgent(d.agentID, sess, false)
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

func (d *Dispatcher) Usage(src string) (msg.TokenUsage, bool) {
	a := d.Agent(src)
	if a == nil {
		return msg.TokenUsage{}, false
	}
	return a.TokenUsage(), true
}

func (d *Dispatcher) Start(ctx context.Context) {
	conn := d.deps.Bus.RegisterAgent(d.agentID, false)
	go func() {
		for {
			select {
			case m, ok := <-conn.Send:
				if !ok {
					return
				}
				if m.To != bus.AddrBroadcast && m.To != d.agentID {
					continue
				}
				switch m.Type {
				case bus.TypeUserInput, bus.TypeSessionReset, bus.TypeSessionCompact, bus.TypeInterrupt:
					d.Handle(ctx, m)
				case bus.TypeTaskDone:
					d.HandleTaskDone(ctx, m)
				case bus.TypeCronTrigger:
					go d.HandleCronTrigger(ctx, m)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (d *Dispatcher) HandleCronTrigger(ctx context.Context, m bus.Msg) {
	src := m.From
	prompt, ok := m.Payload.(string)
	if !ok || src == "" || prompt == "" {
		return
	}

	sess, _ := d.sm.Create()
	a := d.deps.newAgent(d.agentID, sess, false)
	if d.skillLoader != nil {
		skill.RegisterTools(a.Tools(), d.skillLoader)
	}

	if err := a.Run(ctx, src, prompt); err != nil {
		_ = d.deps.Bus.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    d.agentID,
			To:      src,
			Payload: fmt.Sprintf("cron error: %v", err),
		})
	}
	_ = d.deps.Bus.Pub(bus.Msg{
		Type: bus.TypeTurnDone,
		From: d.agentID,
		To:   src,
	})
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
		Type:    bus.TypeTaskDone,
		From:    src,
		To:      d.agentID,
		Payload: content,
	}:
	default:
		_ = d.deps.Bus.Pub(bus.Msg{
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
