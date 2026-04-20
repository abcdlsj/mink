package collab

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
)

func (m *manager) spawn(ctx context.Context, source, runtime, input string, share bool) (string, error) {
	child := "subtask:" + newID()
	if share {
		if err := m.clone(source, child); err != nil {
			return "", err
		}
	}
	return m.app.HandleInputWithRuntime(ctx, child, runtime, input)
}

func (m *manager) delegate(source, runtime, input string, share, direct bool) string {
	t := m.newTask()
	m.publishTask(bus.DelegateStarted, source, t.id, strings.TrimSpace(input), "")
	go m.runDelegation(t, source, runtime, input, share, direct)
	return t.id
}

func (m *manager) newTask() *task {
	t := &task{id: "task-" + newID(), done: make(chan struct{})}
	m.mu.Lock()
	m.tasks[t.id] = t
	m.mu.Unlock()
	return t
}

func (m *manager) runDelegation(t *task, source, runtime, input string, share, direct bool) {
	defer close(t.done)
	out, err := m.spawn(context.Background(), source, runtime, input, share)
	m.finishTask(t, out, err)
	m.publishTask(taskType(err), source, t.id, out, errString(err))
	if direct {
		m.publishResultNotice(source, t.id, out, err)
	}
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
	if typ == bus.DelegateStarted {
		ev.Text = out
	} else {
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
	defer m.mu.Unlock()
	if m.teams[source] == nil {
		m.teams[source] = map[string]string{}
	}
	m.teams[source][alias] = runtime
}

func (m *manager) pickRuntime(source, target, hint string) string {
	target = strings.TrimSpace(target)
	hint = strings.TrimSpace(strings.ToLower(hint))
	m.mu.Lock()
	defer m.mu.Unlock()
	if source != "" && target != "" {
		if rt := m.teams[source][target]; rt != "" {
			return rt
		}
	}
	switch {
	case target != "":
		return target
	case hint == "claude" || strings.Contains(hint, "anthropic"):
		return "claude"
	case hint == "codex" || strings.Contains(hint, "openai"):
		return "codex"
	default:
		return m.app.Config().Runtime
	}
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
