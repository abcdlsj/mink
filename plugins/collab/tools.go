package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/tool"
)

type spawnTool struct{ m *manager }

func (t spawnTool) Name() string { return "spawn" }
func (t spawnTool) Desc() string {
	return "Run a subtask with a worker agent and return a result message pointer plus short outcome"
}
func (t spawnTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("task", "string", "Task to run"),
		tool.Prop("share_context", "boolean", "Share current context"),
		tool.Prop("direct_output", "boolean", "Publish outcome directly"),
		tool.Prop("runtime", "string", "Worker persona id or registered alias"),
		tool.Required("task"),
	)
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
	out, ok, err := t.m.trySpawnInSpace(ctx, src, in, rt)
	if !ok {
		return "", fmt.Errorf("spawn requires a Space-mapped source (channel / direct chat / agent dm)")
	}
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
	return tool.ObjectSchema(
		tool.Prop("task", "string", "Task to delegate"),
		tool.Prop("target", "string", "Worker persona id or registered alias"),
		tool.StringArrayProp("capabilities", "Capability hints"),
		tool.Prop("share_context", "boolean", "Share current context"),
		tool.Prop("direct_output", "boolean", "Publish outcome directly"),
		tool.Required("task"),
	)
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
	id, ok, err := t.m.tryDelegateInSpace(ctx, src, in.Target, rt, in.Task)
	if !ok {
		return "", fmt.Errorf("delegate requires a Space-mapped source (channel / direct chat / agent dm)")
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("delegation accepted, task_id=%s", id), nil
}

type delegatePollTool struct{ m *manager }

func (t delegatePollTool) Name() string { return "delegate_poll" }
func (t delegatePollTool) Desc() string {
	return "Wait for an async delegation to finish"
}
func (t delegatePollTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("task_id", "string", "Delegation task id"),
		tool.Required("task_id"),
	)
}

func (t delegatePollTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in pollArgs
	if err := decode("delegate_poll", args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return "", fmt.Errorf("task_id is required")
	}
	timeout := time.Duration(t.m.app.Config().Collab.PollTimeoutMS) * time.Millisecond
	return t.m.wait(strings.TrimSpace(in.TaskID), timeout)
}

type cancelTool struct{ m *manager }

func (t cancelTool) Name() string { return "cancel_delegation" }
func (t cancelTool) Desc() string {
	return "Cancel a running async delegation by task_id"
}
func (t cancelTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("task_id", "string", "Delegation task id"),
		tool.Required("task_id"),
	)
}

func (t cancelTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in cancelArgs
	if err := decode("cancel_delegation", args, &in); err != nil {
		return "", err
	}
	id := strings.TrimSpace(in.TaskID)
	if id == "" {
		return "", fmt.Errorf("task_id is required")
	}
	if err := t.m.cancel(id); err != nil {
		return "", err
	}
	return fmt.Sprintf("cancel requested for %s", id), nil
}

type inviteTool struct{ m *manager }

func (t inviteTool) Name() string { return "invite_agent" }
func (t inviteTool) Desc() string {
	return "Bind a visible team alias to a registered persona in the current source"
}
func (t inviteTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("agent_id", "string", "Worker persona id or alias"),
		tool.Prop("role_name", "string", "Visible role name"),
		tool.Prop("role_description", "string", "Role description"),
		tool.Prop("task", "string", "Optional first task"),
		tool.Required("agent_id"),
	)
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
	if strings.TrimSpace(in.Task) == "" {
		return fmt.Sprintf("invited %s backed by %s", alias, rt), nil
	}
	id, ok, err := t.m.tryDelegateInSpaceForAlias(ctx, src, in.AgentID, in.Task)
	if !ok {
		return "", fmt.Errorf("invite_agent task requires a Space-mapped source")
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("invited %s backed by %s, task_id=%s", alias, rt, id), nil
}

type mentionTool struct{ m *manager }

func (t mentionTool) Name() string { return "mention" }
func (t mentionTool) Desc() string {
	return "Ask a bound team alias a question; result is a worker-authored Space message, not a Background Task"
}
func (t mentionTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("agent_id", "string", "Worker persona id or alias"),
		tool.Prop("question", "string", "Question to ask"),
		tool.Required("agent_id", "question"),
	)
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
	out, ok, err := t.m.tryMentionInSpace(ctx, src, in)
	if !ok {
		return "", fmt.Errorf("mention requires a Space-mapped source")
	}
	if err != nil {
		return "", err
	}
	return out, nil
}

type specialistTool struct{ m *manager }

func (t specialistTool) Name() string { return "spawn_specialist" }
func (t specialistTool) Desc() string {
	return "Bind a visible team alias to a registered persona; optional first task creates a Task in the store"
}
func (t specialistTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("role_name", "string", "Visible role name"),
		tool.Prop("role_description", "string", "Role description"),
		tool.Prop("profile_hint", "string", "Runtime selection hint"),
		tool.StringArrayProp("capabilities", "Capability hints"),
		tool.Prop("task", "string", "Optional first task"),
		tool.Prop("agent_id", "string", "Worker persona id or alias"),
		tool.Required("role_name", "role_description"),
	)
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
	if strings.TrimSpace(in.Task) == "" {
		return fmt.Sprintf("spawned %s backed by %s", alias, rt), nil
	}
	aliasArg := strings.TrimSpace(in.AgentID)
	if aliasArg == "" {
		aliasArg = alias
	}
	id, ok, err := t.m.tryDelegateInSpaceForAlias(ctx, src, aliasArg, in.Task)
	if !ok {
		return "", fmt.Errorf("spawn_specialist task requires a Space-mapped source")
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("spawned %s backed by %s, task_id=%s", alias, rt, id), nil
}
