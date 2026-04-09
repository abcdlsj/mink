package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
)

const workerIdleTTL = 5 * time.Minute

const (
	workerEnqueueTimeout     = 2 * time.Second
	workerTaskEnqueueTimeout = 8 * time.Second
	workerStatusFirstDelay   = 12 * time.Second
)

type workerState struct {
	q      chan bus.Msg
	cancel context.CancelFunc
}

type Dispatcher struct {
	deps        AgentDeps
	sm          *session.Manager
	agentID     string
	registry    *Registry
	team        *TeamDispatcher
	agents      map[string]*Agent
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
		agents:      make(map[string]*Agent),
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
	delete(d.agents, src)
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

func (d *Dispatcher) worker(ctx context.Context, src string, q chan bus.Msg) {
	defer func() {
		if r := recover(); r != nil {
			d.pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    d.agentID,
				To:      src,
				Payload: fmt.Sprintf("error: worker panic: %v", r),
			})
			d.pub(bus.Msg{
				Type: bus.TypeTurnDone,
				From: d.agentID,
				To:   src,
			})
			d.removeWorker(src)
		}
	}()

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
			initialInput, ok := m.Payload.(string)
			if !ok {
				d.pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    d.agentID,
					Payload: "error: invalid input payload",
					To:      src,
				})
				d.pub(bus.Msg{
					Type: bus.TypeTurnDone,
					From: d.agentID,
					To:   src,
				})
				continue
			}
			speakerID, err := d.runSourceTurn(ctx, src, m.Type, initialInput, a)
			if err != nil {
				if speakerID == "" {
					speakerID = d.agentID
				}
				d.pub(bus.Msg{
					Type:    bus.TypeAssistant,
					From:    speakerID,
					Payload: fmt.Sprintf("error: %v", err),
					To:      src,
				})
			}
			d.pub(bus.Msg{
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
	d.agents[src] = a
	return a
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
		parentTaskID := parentTurn.TaskID
		if _, err := d.rt.CreateChildTask(ctx, parentTaskID, "cron", prompt, d.agentID, src); err != nil {
			_ = err
		}
	}

	a := d.getOrCreateAgent(src)
	state, err := d.startRun(ctx, src, bus.TypeCronTrigger, prompt, a)
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
	err = a.Run(runCtx, src, prompt)
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

	a := d.getOrCreateAgent(src)
	state, err := d.startRun(ctx, src, bus.TypeDelegate, desc, a)
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
	err = a.Run(runCtx, src, desc)
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

	output := d.lastAssistantOutput(a)
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

func (d *Dispatcher) lastAssistantOutput(a *Agent) string {
	if a == nil || a.Session() == nil {
		return ""
	}
	msgs := a.Session().Messages()
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

func enqueueWorker(parent context.Context, q chan bus.Msg, m bus.Msg, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = workerEnqueueTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case q <- m:
		return true
	case <-parent.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (d *Dispatcher) runWithStatus(ctx context.Context, src, msgType, in string, a *Agent) error {
	errCh := make(chan error, 1)
	go func() {
		if msgType == bus.TypeTaskDone {
			errCh <- a.RunSystem(ctx, src, in)
			return
		}
		errCh <- a.Run(ctx, src, in)
	}()

	first := time.NewTimer(workerStatusFirstDelay)
	defer first.Stop()

	for {
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-first.C:
			if msgType == bus.TypeTaskDone {
				continue
			}
			d.pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    d.agentID,
				To:      src,
				Payload: "[status] still working, please wait...",
			})
		}
	}
}

func (d *Dispatcher) pub(m bus.Msg) {
	if d.deps.Bus == nil {
		return
	}
	_ = d.deps.Bus.Pub(m)
}

func (d *Dispatcher) startRun(ctx context.Context, src, msgType, in string, a *Agent) (rtsqlite.RunState, error) {
	if d.rt == nil || a == nil || a.Session() == nil {
		return rtsqlite.RunState{}, nil
	}
	return d.startRunForAgent(ctx, src, a.Session().ID(), d.agentID, msgType, in)
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

func (d *Dispatcher) prepareTeamTurn(ctx context.Context, src string, a *Agent) (TeamTurn, func(), error) {
	if d.team == nil || a == nil {
		return TeamTurn{}, nil, nil
	}
	return d.team.Prepare(ctx, src, a.Session())
}

func (d *Dispatcher) runSourceTurn(ctx context.Context, src, msgType, initialInput string, a *Agent) (string, error) {
	currentInput := initialInput
	lastSpeakerID := d.agentID
	for {
		teamTurn, release, err := d.prepareTeamTurn(ctx, src, a)
		if err != nil {
			return lastSpeakerID, err
		}
		runSource := src
		runAgentID := d.agentID
		runInput := currentInput
		runAgent := a
		if release != nil {
			runSource = teamTurn.RuntimeSource
			runAgentID = teamTurn.SpeakerAgentID
			runAgent = d.teamAgent(teamTurn, a.Session())
			if strings.TrimSpace(teamTurn.Prompt) != "" {
				runInput = teamTurn.Prompt
			}
		}
		lastSpeakerID = runAgentID
		state, err := d.startRunForAgent(ctx, runSource, runAgent.Session().ID(), runAgentID, msgType, runInput)
		if err != nil {
			if release != nil {
				release()
			}
			return lastSpeakerID, err
		}
		runCtx := withRuntimeTurn(ctx, state, runSource)
		if release != nil {
			runCtx = withTeamTurn(runCtx, teamTurn)
		}
		restore := d.setActiveAgent(src, runAgent)
		err = d.runWithStatus(runCtx, src, msgType, runInput, runAgent)
		restore()
		_ = d.finishRun(ctx, state, err)
		if release != nil {
			d.team.Complete(runCtx, teamTurn, d.lastAssistantOutput(runAgent), err)
			release()
		}
		if err != nil {
			return lastSpeakerID, err
		}
		handoff, ok := d.team.Pending(src)
		if !ok {
			if release != nil {
				handoff, ok, err = d.team.AutoSchedule(ctx, src, teamTurn)
				if err != nil {
					return lastSpeakerID, err
				}
			}
		}
		if !ok {
			return lastSpeakerID, nil
		}
		currentInput = handoff.Prompt
	}
}

func (d *Dispatcher) handleTeamMention(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "invalid mention payload"}}, nil
	}
	src := payload["source"]
	targetAgentID := payload["agent_id"]
	question := payload["question"]
	if src == "" || targetAgentID == "" || question == "" {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source, agent_id, question are required"}}, nil
	}
	if d.team == nil {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "team runtime unavailable"}}, nil
	}
	if _, ok := d.team.Binding(src); !ok {
		return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source is not in active team thread"}}, nil
	}
	d.team.Schedule(src, targetAgentID, question)
	return bus.Msg{Type: bus.TypeTeamMention, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"status": "ok"}}, nil
}

func (d *Dispatcher) handleTeamInvite(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]string)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "invalid invite payload"}}, nil
	}
	src := payload["source"]
	agentID := payload["agent_id"]
	roleName := payload["role_name"]
	roleDescription := payload["role_description"]
	task := payload["task"]
	if src == "" || agentID == "" {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source and agent_id are required"}}, nil
	}
	if roleName == "" {
		roleName = agentID
	}
	if d.team == nil || d.rt == nil {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "team runtime unavailable"}}, nil
	}
	binding, ok := d.team.Binding(src)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source is not in active team thread"}}, nil
	}
	if err := d.rt.AddTeamMember(ctx, binding.TeamID, agentID, roleName, roleDescription, "persistent"); err != nil {
		return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	if identity, err := d.rt.GetAgentIdentity(ctx, agentID); err == nil && identity.AgentID == "" {
		if err := d.rt.UpsertAgentIdentity(ctx, agentID, roleName, firstNonEmpty(roleDescription, roleName), "team:"+binding.TeamID); err != nil {
			return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
		}
	}
	if strings.TrimSpace(task) != "" {
		d.team.Schedule(src, agentID, task)
	}
	return bus.Msg{Type: bus.TypeTeamInvite, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"status": "ok"}}, nil
}

func (d *Dispatcher) handleTeamSpawn(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	payload, ok := m.Payload.(map[string]any)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "invalid spawn payload"}}, nil
	}
	src, _ := payload["source"].(string)
	roleName, _ := payload["role_name"].(string)
	roleDescription, _ := payload["role_description"].(string)
	profileHint, _ := payload["profile_hint"].(string)
	task, _ := payload["task"].(string)
	requestedAgentID, _ := payload["agent_id"].(string)
	var capabilities []string
	if rawCaps, ok := payload["capabilities"].([]any); ok {
		for _, cap := range rawCaps {
			if s, ok := cap.(string); ok && strings.TrimSpace(s) != "" {
				capabilities = append(capabilities, strings.TrimSpace(s))
			}
		}
	}
	if src == "" || roleName == "" || roleDescription == "" {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source, role_name, role_description are required"}}, nil
	}
	if d.team == nil || d.rt == nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "team runtime unavailable"}}, nil
	}
	binding, ok := d.team.Binding(src)
	if !ok {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": "source is not in active team thread"}}, nil
	}
	runtimeAgentID, err := d.resolveSpecialistRuntimeAgent(requestedAgentID, profileHint, capabilities)
	if err != nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	agentID := d.specialistAlias(ctx, binding.TeamID, roleName)
	if err := d.rt.AddTeamMemberWithProfile(ctx, binding.TeamID, agentID, roleName, roleDescription, "ephemeral", rtsqlite.TeamMemberProfile{
		RuntimeAgentID: runtimeAgentID,
		ProfileHint:    profileHint,
	}); err != nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	profile := strings.TrimSpace(profileHint)
	if profile == "" {
		profile = strings.TrimSpace(roleDescription)
	}
	if err := d.rt.UpsertAgentIdentity(ctx, agentID, roleName, profile, "team:"+binding.TeamID); err != nil {
		return bus.Msg{Type: bus.TypeTeamSpawn, From: d.agentID, To: m.From, ReplyTo: m.ID, Payload: map[string]string{"error": err.Error()}}, nil
	}
	if strings.TrimSpace(task) != "" {
		d.team.Schedule(src, agentID, task)
	}
	return bus.Msg{
		Type:    bus.TypeTeamSpawn,
		From:    d.agentID,
		To:      m.From,
		ReplyTo: m.ID,
		Payload: map[string]string{
			"status":           "ok",
			"agent_id":         agentID,
			"runtime_agent_id": runtimeAgentID,
		},
	}, nil
}

func (d *Dispatcher) teamAgent(turn TeamTurn, sess *session.Session) *Agent {
	if sess == nil {
		return nil
	}
	desc := d.runtimeDescriptor(turn)
	provider, sel := d.runtimeProviders(desc.Model)
	prompt := strings.TrimSpace(strings.Join([]string{d.deps.Prompt, desc.Prompt, teamSpecialistPrompt(turn)}, "\n\n"))
	agentOpts := []Option{
		WithBus(d.deps.Bus),
		WithSel(sel),
		WithHooks(d.deps.Hooks),
		WithToolGuard(d.deps.ToolGuard),
		WithPrompt(prompt),
		WithConfig(d.deps.Config),
		WithSessionDir(d.deps.SessionDir),
		WithRuntimeDB(d.deps.RuntimeDB),
		WithMemoryStore(d.deps.Memory),
		WithProvider(provider),
		WithSoulPath(desc.SoulPath),
		WithSubAgent(false),
	}
	if d.deps.CronTool != nil {
		agentOpts = append(agentOpts, WithCronTool(d.deps.CronTool))
	}
	a := New(turn.SpeakerAgentID, provider, sess, agentOpts...)
	if d.skillLoader != nil {
		skill.RegisterTools(a.Tools(), d.skillLoader)
	}
	return a
}

func (d *Dispatcher) runtimeDescriptor(turn TeamTurn) AgentDescriptor {
	runtimeAgentID := strings.TrimSpace(turn.RuntimeAgentID)
	if runtimeAgentID == "" {
		runtimeAgentID = turn.SpeakerAgentID
	}
	if d.registry != nil {
		if state := d.registry.Get(runtimeAgentID); state != nil {
			return state.Descriptor
		}
	}
	return AgentDescriptor{}
}

func (d *Dispatcher) runtimeProviders(modelName string) (llm.Provider, *llm.Sel) {
	modelName = strings.TrimSpace(modelName)
	switch modelName {
	case "", "default":
		return d.deps.Provider, d.deps.Sel
	case "cheap":
		if d.deps.Sel != nil {
			return d.deps.Sel.P("cheap"), nil
		}
		return d.deps.Provider, d.deps.Sel
	}
	cfg := d.deps.Config
	if !config.ResolveModel(&cfg, modelName) {
		return d.deps.Provider, d.deps.Sel
	}
	provider, err := llm.NewProvider(llm.Config{
		Provider:  cfg.Active.Provider,
		APIKey:    cfg.Active.APIKey,
		BaseURL:   cfg.Active.BaseURL,
		Model:     cfg.Active.Model,
		Headers:   cfg.Active.Headers,
		MaxTokens: cfg.Active.MaxTokens,
		Reasoning: cfg.Active.Reasoning,
	})
	if err != nil {
		return d.deps.Provider, d.deps.Sel
	}
	return provider, nil
}

func (d *Dispatcher) resolveSpecialistRuntimeAgent(requestedAgentID, profileHint string, capabilities []string) (string, error) {
	if d.registry != nil {
		if requestedAgentID != "" {
			if state := d.registry.Get(requestedAgentID); state != nil {
				return state.Descriptor.ID, nil
			}
		}
		if len(capabilities) > 0 {
			state, err := d.registry.Route(capabilities)
			if err == nil {
				return state.Descriptor.ID, nil
			}
		}
		if candidate := d.matchRegistryAgent(profileHint); candidate != "" {
			return candidate, nil
		}
		available := d.registry.Available()
		if len(available) > 0 {
			return available[0].Descriptor.ID, nil
		}
	}
	if strings.TrimSpace(requestedAgentID) != "" {
		return strings.TrimSpace(requestedAgentID), nil
	}
	if strings.TrimSpace(d.agentID) != "" {
		return d.agentID, nil
	}
	return bus.AddrAgentMain, nil
}

func (d *Dispatcher) matchRegistryAgent(profileHint string) string {
	if d.registry == nil {
		return ""
	}
	hint := strings.ToLower(strings.TrimSpace(profileHint))
	if hint == "" {
		return ""
	}
	if state := d.registry.Get(hint); state != nil {
		return state.Descriptor.ID
	}
	for _, state := range d.registry.All() {
		desc := state.Descriptor
		if strings.EqualFold(desc.Name, hint) || strings.EqualFold(desc.Model, hint) {
			return desc.ID
		}
		for _, cap := range desc.Capabilities {
			if strings.EqualFold(cap, hint) {
				return desc.ID
			}
		}
	}
	for _, state := range d.registry.All() {
		desc := state.Descriptor
		if strings.Contains(strings.ToLower(desc.ID), hint) || strings.Contains(strings.ToLower(desc.Name), hint) {
			return desc.ID
		}
		for _, cap := range desc.Capabilities {
			if strings.Contains(strings.ToLower(cap), hint) {
				return desc.ID
			}
		}
	}
	return ""
}

func (d *Dispatcher) specialistAlias(ctx context.Context, teamID, roleName string) string {
	base := sanitizeAlias(roleName)
	if base == "" {
		base = "specialist"
	}
	prefix := "agent:team:" + shortAlias(teamID) + ":"
	members, err := d.rt.ListTeamMembers(ctx, teamID)
	if err != nil {
		return prefix + base
	}
	existing := make(map[string]struct{}, len(members))
	for _, member := range members {
		existing[member.AgentID] = struct{}{}
	}
	candidate := prefix + base
	if _, ok := existing[candidate]; !ok {
		return candidate
	}
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s%s-%d", prefix, base, i)
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
}

func sanitizeAlias(roleName string) string {
	roleName = strings.ToLower(strings.TrimSpace(roleName))
	var b strings.Builder
	lastDash := false
	for _, r := range roleName {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func shortAlias(teamID string) string {
	teamID = strings.TrimSpace(teamID)
	if len(teamID) <= 8 {
		return teamID
	}
	return teamID[len(teamID)-8:]
}

func teamSpecialistPrompt(turn TeamTurn) string {
	var lines []string
	if role := strings.TrimSpace(turn.SpeakerRole); role != "" {
		lines = append(lines, "Current specialist role: "+role)
	}
	if desc := strings.TrimSpace(turn.SpeakerRoleDesc); desc != "" {
		lines = append(lines, "Current specialist scope: "+desc)
	}
	if profile := strings.TrimSpace(turn.SpeakerProfile); profile != "" {
		lines = append(lines, "Current specialist profile hint: "+profile)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (d *Dispatcher) setActiveAgent(src string, a *Agent) func() {
	d.mu.Lock()
	prev := d.agents[src]
	d.agents[src] = a
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		if prev != nil {
			d.agents[src] = prev
		} else {
			delete(d.agents, src)
		}
		d.mu.Unlock()
	}
}
