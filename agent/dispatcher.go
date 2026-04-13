package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
)

type Dispatcher struct {
	deps        AgentDeps
	sm          *session.Manager
	agentID     string
	registry    *Registry
	team        *TeamDispatcher
	runtimes    map[string]*NativeRuntime
	workers     map[string]*workerState
	rt          *rtsqlite.DB
	skillLoader *skill.Loader
	mu          sync.RWMutex
}

func NewDispatcher(deps AgentDeps, sm *session.Manager, sl *skill.Loader, rt *rtsqlite.DB) *Dispatcher {
	return &Dispatcher{
		deps:        deps,
		sm:          sm,
		agentID:     bus.AddrAgentMain,
		team:        NewTeamDispatcher(rt, deps.Memory, sm),
		runtimes:    make(map[string]*NativeRuntime),
		workers:     make(map[string]*workerState),
		rt:          rt,
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

func (d *Dispatcher) SetRegistry(r *Registry) {
	d.mu.Lock()
	d.registry = r
	d.mu.Unlock()
}

func (d *Dispatcher) Registry() *Registry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.registry
}

func (d *Dispatcher) ResetAgents() {
	d.mu.Lock()
	d.runtimes = make(map[string]*NativeRuntime)
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
		a := d.getOrCreateRuntime(src)
		out, err := a.Agent().Compact(ctx, src, note)
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
			if rt, ok := d.runtimes[m.From]; ok {
				rt.Interrupt()
			}
		} else {
			for _, rt := range d.runtimes {
				rt.Interrupt()
			}
		}
		d.mu.RUnlock()
		return bus.Msg{}, nil
	}

	if m.Type == bus.TypeDelegate {
		return d.handleDelegate(ctx, m)
	}
	if m.Type == bus.TypeTeamMention {
		return d.handleTeamMention(ctx, m)
	}
	if m.Type == bus.TypeTeamInvite {
		return d.handleTeamInvite(ctx, m)
	}
	if m.Type == bus.TypeTeamSpawn {
		return d.handleTeamSpawn(ctx, m)
	}

	src := m.From
	if src == "" {
		return d.inputError(m.From, "invalid source"), nil
	}
	if _, ok := m.Payload.(string); !ok {
		return d.inputError(src, "input payload must be string"), nil
	}
	w := d.ensureWorker(ctx, src)

	if enqueueWorker(ctx, w.q, m, workerEnqueueTimeout) {
		return bus.Msg{}, nil
	}
	d.pub(bus.Msg{
		Type:    bus.TypeAssistant,
		From:    d.agentID,
		Payload: "busy",
		To:      src,
	})
	d.pub(bus.Msg{
		Type: bus.TypeTurnDone,
		From: d.agentID,
		To:   src,
	})
	return bus.Msg{}, nil
}

func (d *Dispatcher) resetAgent(src string) error {
	d.InvalidateSource(src)
	if d.rt != nil {
		if err := d.rt.ResetSource(context.Background(), src); err != nil {
			return err
		}
	}
	_, err := d.sm.ResetSource(src)
	return err
}

func (d *Dispatcher) InvalidateSource(src string) {
	var cancel context.CancelFunc

	d.mu.Lock()
	delete(d.runtimes, src)
	if w, ok := d.workers[src]; ok {
		cancel = w.cancel
		delete(d.workers, src)
	}
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (d *Dispatcher) BindTeamSource(src, teamID, threadID string) {
	if d.team == nil {
		return
	}
	d.team.BindSource(src, teamID, threadID)
	if d.rt != nil {
		_ = d.rt.UpsertTeamSourceBinding(context.Background(), src, teamID, threadID)
	}
}

func (d *Dispatcher) UnbindTeamSource(src string) {
	if d.team == nil {
		return
	}
	d.team.UnbindSource(src)
	if d.rt != nil {
		_ = d.rt.ClearTeamSourceBinding(context.Background(), src)
	}
}

func (d *Dispatcher) getOrCreateRuntime(src string) *NativeRuntime {
	d.mu.RLock()
	if rt, ok := d.runtimes[src]; ok {
		d.mu.RUnlock()
		return rt
	}
	d.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if rt, ok := d.runtimes[src]; ok {
		return rt
	}

	sess, err := d.sm.Current(src)
	if err != nil {
		sess, _ = d.sm.Create()
	}
	if d.rt != nil && sess.EntryCount() == 0 {
		if msgs, err := d.rt.MessagesForSource(context.Background(), src, 200); err == nil {
			session.RestoreMessages(sess, msgs)
		}
	}
	a := d.deps.newAgent(d.agentID, sess, false)
	if d.skillLoader != nil {
		skill.RegisterTools(a.Tools(), d.skillLoader)
	}
	rt := NewNativeRuntime(a)
	rt.source = src
	d.runtimes[src] = rt
	return rt
}

func (d *Dispatcher) Agent(src string) *Agent {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if rt, ok := d.runtimes[src]; ok {
		return rt.Agent()
	}
	return nil
}

func (d *Dispatcher) Usage(src string) (msg.TokenUsage, bool) {
	a := d.Agent(src)
	if a == nil {
		return msg.TokenUsage{}, false
	}
	return a.TokenUsage(), true
}

func (d *Dispatcher) Start(ctx context.Context) {
	if d.deps.Bus != nil {
		d.deps.Bus.RegisterHandler(bus.TypeDelegate, d.handleDelegate)
		d.deps.Bus.RegisterHandler(bus.TypeTeamMention, d.handleTeamMention)
		d.deps.Bus.RegisterHandler(bus.TypeTeamInvite, d.handleTeamInvite)
		d.deps.Bus.RegisterHandler(bus.TypeTeamSpawn, d.handleTeamSpawn)
	}
	conn := d.deps.Bus.RegisterAgent(d.agentID)
	if conn == nil {
		return
	}
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
				case bus.TypeUserInput, bus.TypeSessionReset, bus.TypeSessionCompact, bus.TypeInterrupt, bus.TypeDelegate, bus.TypeTeamMention, bus.TypeTeamInvite, bus.TypeTeamSpawn:
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

	if d.rt != nil {
		parentTurn, _ := runtimeTurnFrom(ctx)
		d.rt.CreateChildTask(ctx, parentTurn.TaskID, "cron", prompt, d.agentID, src)
	}

	rt := d.getOrCreateRuntime(src)
	state, err := d.startRun(ctx, src, bus.TypeCronTrigger, prompt, rt)
	if err != nil {
		d.pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    d.agentID,
			To:      src,
			Payload: fmt.Sprintf("cron error: %v", err),
		})
		d.pub(bus.Msg{
			Type: bus.TypeTurnDone,
			From: d.agentID,
			To:   src,
		})
		return
	}

	runCtx := withRuntimeTurn(ctx, state, src)
	err = rt.Send(runCtx, prompt)
	_ = d.finishRun(ctx, state, err)
	if err != nil {
		d.pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    d.agentID,
			To:      src,
			Payload: fmt.Sprintf("cron error: %v", err),
		})
	}
	d.pub(bus.Msg{
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
		return
	}

	w := d.ensureWorker(ctx, src)

	in := bus.Msg{
		Type:    bus.TypeTaskDone,
		From:    src,
		To:      d.agentID,
		Payload: content,
	}
	if enqueueWorker(ctx, w.q, in, workerTaskEnqueueTimeout) {
		return
	}

	d.pub(bus.Msg{
		Type:    bus.TypeAssistant,
		From:    d.agentID,
		To:      src,
		Payload: content,
	})
	d.pub(bus.Msg{
		Type: bus.TypeTurnDone,
		From: d.agentID,
		To:   src,
	})
}

func (d *Dispatcher) handleDelegate(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]any)
	if !ok {
		return bus.Msg{
			Type:    bus.TypeDelegateAck,
			From:    d.agentID,
			To:      m.From,
			ReplyTo: m.ID,
			Payload: map[string]string{"status": "error", "error": "invalid delegate payload"},
		}, nil
	}

	depth, _ := payload["depth"].(float64)
	if err := CheckDelegationDepth(int(depth)); err != nil {
		return bus.Msg{
			Type:    bus.TypeDelegateAck,
			From:    d.agentID,
			To:      m.From,
			ReplyTo: m.ID,
			Payload: map[string]string{"status": "error", "error": err.Error()},
		}, nil
	}

	desc, _ := payload["description"].(string)
	if desc == "" {
		return bus.Msg{
			Type:    bus.TypeDelegateAck,
			From:    d.agentID,
			To:      m.From,
			ReplyTo: m.ID,
			Payload: map[string]string{"status": "error", "error": "description is required"},
		}, nil
	}

	target, _ := payload["target_agent"].(string)
	targetID := d.agentID
	if target != "" {
		targetID = target
	} else if d.registry != nil {
		caps, _ := payload["capabilities"].([]any)
		capStrs := make([]string, 0, len(caps))
		for _, c := range caps {
			if s, ok := c.(string); ok {
				capStrs = append(capStrs, s)
			}
		}
		if len(capStrs) > 0 {
			state, err := d.registry.Route(capStrs)
			if err != nil {
				return bus.Msg{
					Type:    bus.TypeDelegateAck,
					From:    d.agentID,
					To:      m.From,
					ReplyTo: m.ID,
					Payload: map[string]string{"status": "error", "error": err.Error()},
				}, nil
			}
			targetID = state.Descriptor.ID
		}
	}

	taskID := m.ID

	go d.runDelegation(ctx, m, taskID, targetID, desc, int(depth))

	return bus.Msg{
		Type:    bus.TypeDelegateAck,
		From:    d.agentID,
		To:      m.From,
		ReplyTo: m.ID,
		Payload: map[string]string{"status": "accepted", "task_id": taskID},
	}, nil
}

func (d *Dispatcher) runDelegation(ctx context.Context, m bus.Msg, taskID, targetID, desc string, depth int) {
	src := fmt.Sprintf("delegate:%s:%s", m.From, taskID)

	if d.rt != nil {
		parentTurn, _ := runtimeTurnFrom(ctx)
		parentTaskID := parentTurn.TaskID
		if childID, err := d.rt.CreateChildTask(ctx, parentTaskID, "delegation", desc, targetID, src); err == nil && childID != "" {
			taskID = childID
		}
	}

	rt := d.getOrCreateRuntime(src)
	state, err := d.startRun(ctx, src, bus.TypeDelegate, desc, rt)
	if err != nil {
		d.pub(bus.Msg{
			Type:    bus.TypeDelegateResult,
			From:    targetID,
			To:      m.From,
			ReplyTo: taskID,
			Payload: map[string]string{
				"task_id": taskID,
				"status":  "error",
				"error":   fmt.Sprintf("start run: %v", err),
			},
		})
		return
	}

	runCtx := withRuntimeTurn(ctx, state, src)
	runCtx = bus.WithDelegationDepth(runCtx, depth+1)
	err = rt.Send(runCtx, desc)
	_ = d.finishRun(ctx, state, err)

	if err != nil {
		d.pub(bus.Msg{
			Type:    bus.TypeDelegateResult,
			From:    targetID,
			To:      m.From,
			ReplyTo: taskID,
			Payload: map[string]string{
				"task_id": taskID,
				"status":  "error",
				"error":   err.Error(),
			},
		})
		return
	}

	output := d.lastAssistantOutput(rt)
	d.pub(bus.Msg{
		Type:    bus.TypeDelegateResult,
		From:    targetID,
		To:      m.From,
		ReplyTo: taskID,
		Payload: map[string]string{
			"task_id": taskID,
			"status":  "ok",
			"output":  output,
		},
	})
}

func (d *Dispatcher) lastAssistantOutput(rt Runtime) string {
	if rt == nil || rt.Session() == nil {
		return ""
	}
	msgs := rt.Session().Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
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

func (d *Dispatcher) pub(m bus.Msg) {
	if d.deps.Bus == nil {
		return
	}
	_ = d.deps.Bus.Pub(m)
}

func (d *Dispatcher) startRun(ctx context.Context, src, msgType, in string, rt Runtime) (rtsqlite.RunState, error) {
	if d.rt == nil || rt == nil || rt.Session() == nil {
		return rtsqlite.RunState{}, nil
	}
	return d.startRunForAgent(ctx, src, rt.Session().ID(), d.agentID, msgType, in)
}

func (d *Dispatcher) finishRun(ctx context.Context, state rtsqlite.RunState, err error) error {
	if d.rt == nil {
		return nil
	}
	return d.rt.FinishRun(ctx, state, err)
}

func trigger(msgType string) string {
	switch msgType {
	case bus.TypeTaskDone:
		return "system"
	case bus.TypeCronTrigger:
		return "cron"
	default:
		return "user_input"
	}
}

func (d *Dispatcher) startRunForAgent(ctx context.Context, source, sessionID, agentID, msgType, in string) (rtsqlite.RunState, error) {
	if d.rt == nil {
		return rtsqlite.RunState{}, nil
	}
	return d.rt.StartRun(ctx, source, sessionID, agentID, trigger(msgType), in)
}

func (d *Dispatcher) setActiveRuntime(src string, rt *NativeRuntime) func() {
	d.mu.Lock()
	prev := d.runtimes[src]
	d.runtimes[src] = rt
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		if prev != nil {
			d.runtimes[src] = prev
		} else {
			delete(d.runtimes, src)
		}
		d.mu.Unlock()
	}
}
