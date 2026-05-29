package collab

import (
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/msg"
)

func (m *manager) cancel(id string) error {
	if m.app == nil || m.app.Tasks() == nil {
		return fmt.Errorf("task not found: %s", id)
	}
	tk, _ := m.app.Tasks().Get(id)
	if tk == nil {
		return fmt.Errorf("task not found: %s", id)
	}
	return m.app.Tasks().Cancel(id)
}

func (m *manager) wait(id string, timeout time.Duration) (string, error) {
	if m.app == nil || m.app.Tasks() == nil {
		return "", fmt.Errorf("task not found: %s", id)
	}
	tk, err := m.app.Tasks().Get(id)
	if err != nil || tk == nil {
		return "", fmt.Errorf("task not found: %s", id)
	}
	deadline := time.Now().Add(timeout)
	for {
		switch tk.Status {
		case "finished":
			return tk.Outcome, nil
		case "failed":
			return "", fmt.Errorf("%s", strings.TrimSpace(tk.Outcome))
		case "canceled":
			return "", fmt.Errorf("canceled: %s", strings.TrimSpace(tk.Outcome))
		case "empty_output":
			return tk.Outcome, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("delegation timed out after %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
		tk, err = m.app.Tasks().Get(id)
		if err != nil || tk == nil {
			return "", fmt.Errorf("task not found: %s", id)
		}
	}
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
