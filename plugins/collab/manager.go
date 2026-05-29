package collab

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
)

var errQueueFull = errors.New("delegate queue full, retry later")

func (m *manager) spawn(ctx context.Context, source, runtime, input string, share bool) (string, error) {
	return m.spawnWithChild(ctx, source, runtime, input, share, "subtask:"+newID())
}

// spawnWithChild lets callers (like runDelegation) pin a deterministic
// child source so the resulting sub-session can be correlated back to
// the originating task by the desktop UI / replay layer.
func (m *manager) spawnWithChild(ctx context.Context, source, runtime, input string, share bool, child string) (string, error) {
	if share {
		if err := m.clone(source, child); err != nil {
			return "", err
		}
	}
	return m.app.HandleInputWithRuntime(ctx, child, runtime, input)
}

func (m *manager) delegate(source, runtime, input string, share, direct bool) (string, error) {
	select {
	case m.queue <- struct{}{}:
	default:
		return "", errQueueFull
	}
	t := m.newTask(source)
	m.publishTask(bus.DelegateQueued, source, t.id, strings.TrimSpace(input), "")
	go m.runDelegation(t, runtime, input, share, direct)
	return t.id, nil
}

func (m *manager) newTask(source string) *task {
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{
		id:     "task-" + newID(),
		source: source,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	t.ctx = ctx
	m.mu.Lock()
	m.tasks[t.id] = t
	m.mu.Unlock()
	return t
}

func (m *manager) runDelegation(t *task, runtime, input string, share, direct bool) {
	defer close(t.done)
	defer func() { <-m.queue }()

	select {
	case m.sem <- struct{}{}:
	case <-t.ctx.Done():
		m.finishTask(t, "", t.ctx.Err())
		m.publishTask(bus.DelegateCanceled, t.source, t.id, "", errString(t.ctx.Err()))
		return
	}
	defer func() { <-m.sem }()

	m.publishTask(bus.DelegateStarted, t.source, t.id, strings.TrimSpace(input), "")
	child := "subtask:" + t.id
	out, err := m.spawnWithChild(t.ctx, t.source, runtime, input, share, child)
	m.finishTask(t, out, err)
	m.publishTask(taskType(t.ctx, err), t.source, t.id, out, errString(err))
	if direct {
		m.publishResultNotice(t.source, t.id, out, err)
	}
}

func (m *manager) cancel(id string) error {
	if m.app != nil && m.app.Tasks() != nil {
		if tk, _ := m.app.Tasks().Get(id); tk != nil {
			return m.app.Tasks().Cancel(id)
		}
	}
	t := m.task(id)
	if t == nil {
		return fmt.Errorf("task not found: %s", id)
	}
	t.cancel()
	return nil
}

func (m *manager) finishTask(t *task, out string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.output = out
	t.err = err
}

func (m *manager) publishTask(typ, source, id, out, err string) {
	ev := bus.Event{
		Type:   typ,
		Source: source,
		TaskID: id,
	}
	switch typ {
	case bus.DelegateQueued, bus.DelegateStarted:
		ev.Text = out
	default:
		ev.Output = out
		ev.Err = err
	}
	m.app.Bus().Publish(ev)
}

func (m *manager) publishResultNotice(source, id, out string, err error) {
	if err != nil {
		m.app.PublishNotice(source, fmt.Sprintf("[%s] error: %s", id, err))
		return
	}
	if strings.TrimSpace(out) != "" {
		m.app.PublishNotice(source, fmt.Sprintf("[%s]\n%s", id, out))
	}
}

func (m *manager) wait(id string, timeout time.Duration) (string, error) {
	if out, ok, err := m.waitTaskStore(id, timeout); ok {
		return out, err
	}
	t := m.task(id)
	if t == nil {
		return m.waitPersisted(id)
	}
	select {
	case <-t.done:
	case <-time.After(timeout):
		return "", fmt.Errorf("delegation timed out after %s", timeout)
	}
	return t.result()
}

func (m *manager) waitTaskStore(id string, timeout time.Duration) (string, bool, error) {
	if m.app == nil || m.app.Tasks() == nil {
		return "", false, nil
	}
	tk, err := m.app.Tasks().Get(id)
	if err != nil || tk == nil {
		return "", false, nil
	}
	deadline := time.Now().Add(timeout)
	for {
		switch tk.Status {
		case "finished":
			return tk.Outcome, true, nil
		case "failed":
			return "", true, fmt.Errorf("%s", strings.TrimSpace(tk.Outcome))
		case "canceled":
			return "", true, fmt.Errorf("canceled: %s", strings.TrimSpace(tk.Outcome))
		case "empty_output":
			return tk.Outcome, true, nil
		}
		if time.Now().After(deadline) {
			return "", true, fmt.Errorf("delegation timed out after %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
		tk, err = m.app.Tasks().Get(id)
		if err != nil || tk == nil {
			return "", true, fmt.Errorf("task not found: %s", id)
		}
	}
}

func (m *manager) waitPersisted(id string) (string, error) {
	evs, err := m.app.ReplayTask(id, 32)
	if err != nil {
		return "", err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		switch evs[i].Type {
		case bus.DelegateFinished:
			return evs[i].Output, nil
		case bus.DelegateFailed:
			return "", fmt.Errorf("%s", strings.TrimSpace(evs[i].Err))
		case bus.DelegateCanceled:
			return "", fmt.Errorf("canceled: %s", strings.TrimSpace(evs[i].Err))
		}
	}
	return "", fmt.Errorf("task not found: %s", id)
}

func (m *manager) task(id string) *task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

func (t *task) result() (string, error) {
	if t == nil {
		return "", nil
	}
	if t.err != nil {
		return "", t.err
	}
	return t.output, nil
}

func (m *manager) clone(parent, child string) error {
	src, err := m.app.CurrentSession(parent)
	if err != nil {
		return err
	}
	dst, err := m.app.NewSession(child)
	if err != nil {
		return err
	}
	dst.Title = src.Title
	dst.Summary = src.Summary
	dst.Messages = cloneMessages(src.Messages)
	return m.app.SaveSession(dst)
}

func (m *manager) bind(source, alias, runtime string) {
	source = strings.TrimSpace(source)
	alias = strings.TrimSpace(alias)
	runtime = strings.TrimSpace(runtime)
	if source == "" || alias == "" || runtime == "" {
		return
	}
	m.mu.Lock()
	if m.teams[source] == nil {
		m.teams[source] = map[string]string{}
	}
	m.teams[source][alias] = runtime
	snap := cloneTeams(m.teams)
	m.mu.Unlock()
	if err := m.saveTeams(snap); err != nil {
		m.app.PublishNotice(source, fmt.Sprintf("collab: persist teams failed: %s", err))
	}
}

func (m *manager) pickRuntime(source, target, hint string) string {
	target = strings.TrimSpace(target)
	hint = strings.TrimSpace(strings.ToLower(hint))
	m.mu.Lock()
	if source != "" && target != "" {
		if rt := m.teams[source][target]; rt != "" {
			m.mu.Unlock()
			return rt
		}
	}
	m.mu.Unlock()
	candidate := ""
	switch {
	case target != "":
		candidate = target
	case hint == "claude" || strings.Contains(hint, "anthropic"):
		candidate = "claude"
	case hint == "codex" || strings.Contains(hint, "openai"):
		candidate = "codex"
	}
	if candidate != "" && m.app.HasRuntime(candidate) {
		return candidate
	}
	fallback := m.app.Config().Runtime
	if candidate != "" && candidate != fallback {
		m.app.PublishNotice(source, fmt.Sprintf("collab: runtime %q unavailable, using %q", candidate, fallback))
	}
	return fallback
}

func cloneMessages(in []msg.Message) []msg.Message {
	out := make([]msg.Message, 0, len(in))
	for _, m := range in {
		cp := m
		if len(m.ToolCalls) > 0 {
			cp.ToolCalls = append([]msg.ToolCall(nil), m.ToolCalls...)
		}
		if len(m.ToolResults) > 0 {
			cp.ToolResults = append([]msg.ToolResult(nil), m.ToolResults...)
		}
		out = append(out, cp)
	}
	return out
}

func cloneTeams(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for k, v := range in {
		cp := make(map[string]string, len(v))
		for a, r := range v {
			cp[a] = r
		}
		out[k] = cp
	}
	return out
}
