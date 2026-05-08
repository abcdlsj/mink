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
	return "Run a subtask with a child agent and return its final result"
}
func (t spawnTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("task", "string", "Task to run"),
		tool.Prop("share_context", "boolean", "Share current context"),
		tool.Prop("direct_output", "boolean", "Publish output directly"),
		tool.Prop("runtime", "string", "Runtime name"),
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
	return tool.ObjectSchema(
		tool.Prop("task", "string", "Task to delegate"),
		tool.Prop("target", "string", "Target runtime or alias"),
		tool.StringArrayProp("capabilities", "Capability hints"),
		tool.Prop("share_context", "boolean", "Share current context"),
		tool.Prop("direct_output", "boolean", "Publish output directly"),
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
	id := t.m.delegate(src, rt, in.Task, in.ShareContext, in.DirectOutput)
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
	return t.m.wait(strings.TrimSpace(in.TaskID), 2*time.Minute)
}

type inviteTool struct{ m *manager }

func (t inviteTool) Name() string { return "invite_agent" }
func (t inviteTool) Desc() string {
	return "Bind a visible team alias to a runtime in the current source"
}
func (t inviteTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("agent_id", "string", "Runtime or alias"),
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
	return tool.ObjectSchema(
		tool.Prop("agent_id", "string", "Runtime or alias"),
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
	return tool.ObjectSchema(
		tool.Prop("role_name", "string", "Visible role name"),
		tool.Prop("role_description", "string", "Role description"),
		tool.Prop("profile_hint", "string", "Runtime selection hint"),
		tool.StringArrayProp("capabilities", "Capability hints"),
		tool.Prop("task", "string", "Optional first task"),
		tool.Prop("agent_id", "string", "Runtime or alias"),
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
	if strings.TrimSpace(in.Task) != "" {
		id := t.m.delegate(src, rt, in.Task, true, true)
		return fmt.Sprintf("spawned %s backed by %s, task_id=%s", alias, rt, id), nil
	}
	return fmt.Sprintf("spawned %s backed by %s", alias, rt), nil
}
