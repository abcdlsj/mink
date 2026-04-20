package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/msg"
)

type manager struct {
	app   *app.App
	mu    sync.Mutex
	tasks map[string]*task
	teams map[string]map[string]string
}

type task struct {
	id     string
	output string
	err    error
	done   chan struct{}
}

type spawnArgs struct {
	Task         string `json:"task"`
	ShareContext bool   `json:"share_context"`
	DirectOutput bool   `json:"direct_output"`
	Runtime      string `json:"runtime"`
}

type delegateArgs struct {
	Task         string   `json:"task"`
	Target       string   `json:"target"`
	Capabilities []string `json:"capabilities"`
	ShareContext bool     `json:"share_context"`
	DirectOutput bool     `json:"direct_output"`
}

type inviteArgs struct {
	AgentID         string `json:"agent_id"`
	RoleName        string `json:"role_name"`
	RoleDescription string `json:"role_description"`
	Task            string `json:"task"`
}

type mentionArgs struct {
	AgentID  string `json:"agent_id"`
	Question string `json:"question"`
}

type pollArgs struct {
	TaskID string `json:"task_id"`
}

type specialistArgs struct {
	RoleName        string   `json:"role_name"`
	RoleDescription string   `json:"role_description"`
	ProfileHint     string   `json:"profile_hint"`
	Capabilities    []string `json:"capabilities"`
	Task            string   `json:"task"`
	AgentID         string   `json:"agent_id"`
}

func Plugin() app.Plugin {
	return func(a *app.App) error {
		m := &manager{
			app:   a,
			tasks: map[string]*task{},
			teams: map[string]map[string]string{},
		}
		a.RegisterTool(spawnTool{m: m})
		a.RegisterTool(delegateTool{m: m})
		a.RegisterTool(delegatePollTool{m: m})
		a.RegisterTool(inviteTool{m: m})
		a.RegisterTool(mentionTool{m: m})
		a.RegisterTool(specialistTool{m: m})
		return nil
	}
}

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
	t := &task{
		id:   "task-" + newID(),
		done: make(chan struct{}),
	}
	m.mu.Lock()
	m.tasks[t.id] = t
	m.mu.Unlock()

	go func() {
		defer close(t.done)
		out, err := m.spawn(context.Background(), source, runtime, input, share)
		m.mu.Lock()
		t.output = out
		t.err = err
		m.mu.Unlock()
		if direct {
			if err != nil {
				m.app.PublishNotice(source, fmt.Sprintf("[%s] error: %s", t.id, err))
			} else if strings.TrimSpace(out) != "" {
				m.app.PublishNotice(source, fmt.Sprintf("[%s]\n%s", t.id, out))
			}
		}
	}()
	return t.id
}

func (m *manager) wait(id string, timeout time.Duration) (string, error) {
	t := m.task(id)
	if t == nil {
		return "", fmt.Errorf("task not found: %s", id)
	}
	select {
	case <-t.done:
	case <-time.After(timeout):
		return "", fmt.Errorf("delegation timed out after %s", timeout)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.err != nil {
		return "", t.err
	}
	return t.output, nil
}

func (m *manager) task(id string) *task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
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

func decode[T any](name string, args json.RawMessage, dst *T) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return fmt.Errorf("%s: parse error: %w", name, err)
	}
	return nil
}

func capabilityHint(v []string) string {
	return strings.Join(v, " ")
}

type spawnTool struct{ m *manager }

func (t spawnTool) Name() string { return "spawn" }
func (t spawnTool) Desc() string {
	return "Run a subtask with a child agent and return its final result"
}
func (t spawnTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"task":          map[string]any{"type": "string"},
		"share_context": map[string]any{"type": "boolean"},
		"direct_output": map[string]any{"type": "boolean"},
		"runtime":       map[string]any{"type": "string"},
	}, "required": []string{"task"}}
}
func (t spawnTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in spawnArgs
	if err := decode("spawn", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Task) == "" {
		return "", fmt.Errorf("task is required")
	}
	src := command.SourceFrom(ctx)
	rt := t.m.pickRuntime(src, in.Runtime, in.Runtime)
	out, err := t.m.spawn(ctx, src, rt, in.Task, in.ShareContext)
	if err != nil {
		return "", err
	}
	if in.DirectOutput && strings.TrimSpace(out) != "" {
		t.m.app.PublishNotice(src, out)
	}
	return out, nil
}

type delegateTool struct{ m *manager }

func (t delegateTool) Name() string { return "delegate" }
func (t delegateTool) Desc() string {
	return "Delegate a task asynchronously and return a task id"
}
func (t delegateTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"task":          map[string]any{"type": "string"},
		"target":        map[string]any{"type": "string"},
		"capabilities":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"share_context": map[string]any{"type": "boolean"},
		"direct_output": map[string]any{"type": "boolean"},
	}, "required": []string{"task"}}
}
func (t delegateTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in delegateArgs
	if err := decode("delegate", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Task) == "" {
		return "", fmt.Errorf("task is required")
	}
	src := command.SourceFrom(ctx)
	rt := t.m.pickRuntime(src, in.Target, capabilityHint(in.Capabilities))
	id := t.m.delegate(src, rt, in.Task, in.ShareContext, in.DirectOutput)
	return fmt.Sprintf("delegation accepted, task_id=%s", id), nil
}

type delegatePollTool struct{ m *manager }

func (t delegatePollTool) Name() string { return "delegate_poll" }
func (t delegatePollTool) Desc() string {
	return "Wait for an async delegation to finish"
}
func (t delegatePollTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"task_id": map[string]any{"type": "string"},
	}, "required": []string{"task_id"}}
}
func (t delegatePollTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in pollArgs
	if err := decode("delegate_poll", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return "", fmt.Errorf("task_id is required")
	}
	return t.m.wait(strings.TrimSpace(in.TaskID), 2*time.Minute)
}

type inviteTool struct{ m *manager }

func (t inviteTool) Name() string { return "invite_agent" }
func (t inviteTool) Desc() string {
	return "Bind a visible team alias to a runtime in the current source"
}
func (t inviteTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"agent_id":         map[string]any{"type": "string"},
		"role_name":        map[string]any{"type": "string"},
		"role_description": map[string]any{"type": "string"},
		"task":             map[string]any{"type": "string"},
	}, "required": []string{"agent_id"}}
}
func (t inviteTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in inviteArgs
	if err := decode("invite_agent", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.AgentID) == "" {
		return "", fmt.Errorf("agent_id is required")
	}
	src := command.SourceFrom(ctx)
	alias := strings.TrimSpace(in.RoleName)
	if alias == "" {
		alias = strings.TrimSpace(in.AgentID)
	}
	rt := t.m.pickRuntime(src, in.AgentID, in.AgentID)
	t.m.bind(src, alias, rt)
	if strings.TrimSpace(in.Task) != "" {
		id := t.m.delegate(src, rt, in.Task, true, true)
		return fmt.Sprintf("invited %s backed by %s, task_id=%s", alias, rt, id), nil
	}
	return fmt.Sprintf("invited %s backed by %s", alias, rt), nil
}

type mentionTool struct{ m *manager }

func (t mentionTool) Name() string { return "mention" }
func (t mentionTool) Desc() string {
	return "Route a question to a bound team alias asynchronously"
}
func (t mentionTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"agent_id": map[string]any{"type": "string"},
		"question": map[string]any{"type": "string"},
	}, "required": []string{"agent_id", "question"}}
}
func (t mentionTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in mentionArgs
	if err := decode("mention", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.AgentID) == "" || strings.TrimSpace(in.Question) == "" {
		return "", fmt.Errorf("agent_id and question are required")
	}
	src := command.SourceFrom(ctx)
	rt := t.m.pickRuntime(src, in.AgentID, in.AgentID)
	id := t.m.delegate(src, rt, in.Question, true, true)
	return fmt.Sprintf("scheduled next team turn for %s, task_id=%s", strings.TrimSpace(in.AgentID), id), nil
}

type specialistTool struct{ m *manager }

func (t specialistTool) Name() string { return "spawn_specialist" }
func (t specialistTool) Desc() string {
	return "Create a team alias backed by a runtime and optionally schedule its first task"
}
func (t specialistTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"role_name":        map[string]any{"type": "string"},
		"role_description": map[string]any{"type": "string"},
		"profile_hint":     map[string]any{"type": "string"},
		"capabilities":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"task":             map[string]any{"type": "string"},
		"agent_id":         map[string]any{"type": "string"},
	}, "required": []string{"role_name", "role_description"}}
}
func (t specialistTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in specialistArgs
	if err := decode("spawn_specialist", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RoleName) == "" || strings.TrimSpace(in.RoleDescription) == "" {
		return "", fmt.Errorf("role_name and role_description are required")
	}
	src := command.SourceFrom(ctx)
	hint := strings.TrimSpace(in.ProfileHint)
	if hint == "" && len(in.Capabilities) > 0 {
		hint = capabilityHint(in.Capabilities)
	}
	rt := t.m.pickRuntime(src, in.AgentID, hint)
	alias := strings.TrimSpace(in.RoleName)
	t.m.bind(src, alias, rt)
	if strings.TrimSpace(in.Task) != "" {
		id := t.m.delegate(src, rt, in.Task, true, true)
		return fmt.Sprintf("spawned %s backed by %s, task_id=%s", alias, rt, id), nil
	}
	return fmt.Sprintf("spawned %s backed by %s", alias, rt), nil
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

func newID() string {
	return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
}
