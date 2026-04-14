package agent

import (
	"context"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
)

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

func (d *Dispatcher) getOrCreateRuntime(src string) Runtime {
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

	if driver, ok := d.resolveExternalDriver(); ok {
		sess, err := d.sm.Current(src)
		if err != nil {
			sess, _ = d.sm.Create()
		}
		rt := NewExternalRuntime(ExternalRuntimeConfig{
			Driver:  driver,
			Memory:  d.deps.Memory,
			RT:      d.rt,
			Bus:     d.deps.Bus,
			Session: sess,
			WorkDir: workspacePath(d.rt),
		})
		rt.Start(context.Background(), RuntimeConfig{
			Source:  src,
			AgentID: d.agentID,
			Session: sess,
		})
		d.runtimes[src] = rt
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
		if nr, ok := rt.(*NativeRuntime); ok {
			return nr.Agent()
		}
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

func (d *Dispatcher) setActiveRuntime(src string, rt Runtime) func() {
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

func (d *Dispatcher) ModelDisplayName() string {
	if driver, ok := d.resolveExternalDriver(); ok {
		return driver.Name
	}
	return d.deps.Config.ActiveModel
}

func (d *Dispatcher) resolveExternalDriver() (ExternalDriver, bool) {
	for _, ac := range d.deps.Config.Agents {
		if ac.ID == d.agentID || ac.ID == "" {
			switch ac.Runtime {
			case "claude":
				return ClaudeCodeDriver(), true
			}
		}
	}
	return ExternalDriver{}, false
}

func workspacePath(db *rtsqlite.DB) string {
	if db == nil {
		return ""
	}
	return db.WorkspacePath()
}
