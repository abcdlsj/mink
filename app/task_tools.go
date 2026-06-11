package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
	"github.com/abcdlsj/sumi/tool"
)

const (
	capTaskCreate  = "task.create"
	capTaskAssign  = "task.assign"
	capTaskExecute = "task.execute"
	capTaskReview  = "task.review"
)

func (a *App) registerTaskTools() {
	a.RegisterTool(taskCreateTool{a: a})
	a.RegisterTool(taskAssignTool{a: a})
	a.RegisterTool(taskStatusTool{a: a})
}

type taskCreateTool struct{ a *App }

func (t taskCreateTool) Name() string { return "task_create" }
func (t taskCreateTool) Desc() string {
	return "Create a Task Board item with explicit assignee, expected outcome, acceptance criteria, and source"
}
func (t taskCreateTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("title", "string", "Short task title"),
		tool.Prop("assignee_id", "string", "Persona id assigned to execute the task"),
		tool.Prop("expected_outcome", "string", "Expected outcome before execution"),
		tool.Prop("acceptance_criteria", "string", "Concrete acceptance criteria"),
		tool.Prop("space_id", "string", "Source Space id; defaults to current channel/direct space when available"),
		tool.Prop("source_message_id", "string", "Source message id"),
		tool.Prop("source_thread_id", "string", "Source thread root message id"),
		tool.Required("title", "assignee_id", "expected_outcome", "acceptance_criteria"),
	)
}
func (t taskCreateTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Title              string `json:"title"`
		AssigneeID         string `json:"assignee_id"`
		Assignee           string `json:"assignee"`
		ExpectedOutcome    string `json:"expected_outcome"`
		Outcome            string `json:"outcome"`
		AcceptanceCriteria string `json:"acceptance_criteria"`
		SpaceID            string `json:"space_id"`
		SourceMessageID    string `json:"source_message_id"`
		SourceMessage      string `json:"source_message"`
		SourceThreadID     string `json:"source_thread_id"`
		SourceThread       string `json:"source_thread"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("task_create: %w", err)
	}
	actor, err := t.a.requireTaskCapability(ctx, capTaskCreate, capTaskAssign)
	if err != nil {
		return "", err
	}
	assignee, err := t.a.taskAssignee(firstNonEmpty(in.AssigneeID, in.Assignee))
	if err != nil {
		return "", err
	}
	spaceID, err := t.a.taskSpaceID(ctx, in.SpaceID)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(in.Title)
	expected := firstNonEmpty(in.ExpectedOutcome, in.Outcome)
	criteria := strings.TrimSpace(in.AcceptanceCriteria)
	if title == "" || expected == "" || criteria == "" {
		return "", fmt.Errorf("task_create requires title, expected_outcome, and acceptance_criteria")
	}
	sourceMessageID := firstNonEmpty(in.SourceMessageID, in.SourceMessage)
	sourceThreadID := firstNonEmpty(in.SourceThreadID, in.SourceThread, command.ParentMessageFrom(ctx))
	if sourceMessageID == "" {
		sourceMessageID = sourceThreadID
	}
	tk, err := t.a.tasks.Create(taskpkg.CreateTaskInput{
		SpaceID:            spaceID,
		TriggerMessageID:   sourceMessageID,
		SourceThreadID:     sourceThreadID,
		InitiatorID:        actor.ID,
		CreatedBy:          actor.ID,
		WorkerID:           assignee.ID,
		AssignedBy:         actor.ID,
		Title:              title,
		ExpectedOutcome:    expected,
		AcceptanceCriteria: criteria,
		Source:             command.SourceFrom(ctx),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("task created: %s assigned_to=%s status=%s", tk.ID, tk.WorkerID, tk.Status), nil
}

type taskAssignTool struct{ a *App }

func (t taskAssignTool) Name() string { return "task_assign" }
func (t taskAssignTool) Desc() string {
	return "Assign an existing Task Board item to an executable persona"
}
func (t taskAssignTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("task_id", "string", "Task id"),
		tool.Prop("assignee_id", "string", "Persona id assigned to execute the task"),
		tool.Prop("expected_outcome", "string", "Expected outcome before execution"),
		tool.Prop("acceptance_criteria", "string", "Concrete acceptance criteria"),
		tool.Required("task_id", "assignee_id"),
	)
}
func (t taskAssignTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		TaskID             string `json:"task_id"`
		AssigneeID         string `json:"assignee_id"`
		Assignee           string `json:"assignee"`
		ExpectedOutcome    string `json:"expected_outcome"`
		Outcome            string `json:"outcome"`
		AcceptanceCriteria string `json:"acceptance_criteria"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("task_assign: %w", err)
	}
	actor, err := t.a.requireTaskCapability(ctx, capTaskAssign)
	if err != nil {
		return "", err
	}
	assignee, err := t.a.taskAssignee(firstNonEmpty(in.AssigneeID, in.Assignee))
	if err != nil {
		return "", err
	}
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return "", fmt.Errorf("task_assign requires task_id")
	}
	tk, err := t.a.tasks.Update(taskID, taskpkg.UpdateTaskInput{
		WorkerID:           assignee.ID,
		AssignedBy:         actor.ID,
		ExpectedOutcome:    firstNonEmpty(in.ExpectedOutcome, in.Outcome),
		AcceptanceCriteria: strings.TrimSpace(in.AcceptanceCriteria),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("task assigned: %s assigned_to=%s assigned_by=%s", tk.ID, tk.WorkerID, tk.AssignedBy), nil
}

type taskStatusTool struct{ a *App }

func (t taskStatusTool) Name() string { return "task_update_status" }
func (t taskStatusTool) Desc() string {
	return "Move an assigned task through todo, in_progress, in_review, done, or closed"
}
func (t taskStatusTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("task_id", "string", "Task id"),
		tool.Prop("status", "string", "todo, doing/in_progress, review/in_review, done, or closed"),
		tool.Prop("outcome", "string", "Short result when marking done or closed"),
		tool.Required("task_id", "status"),
	)
}
func (t taskStatusTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		TaskID  string `json:"task_id"`
		Status  string `json:"status"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("task_update_status: %w", err)
	}
	actor, err := t.a.requireTaskStatusCapability(ctx, in.Status)
	if err != nil {
		return "", err
	}
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return "", fmt.Errorf("task_update_status requires task_id")
	}
	tk, err := t.a.tasks.Get(taskID)
	if err != nil {
		return "", err
	}
	if !actor.HasCapability(capTaskAssign) && !actor.HasCapability(capTaskReview) && strings.TrimSpace(tk.WorkerID) != actor.ID {
		return "", fmt.Errorf("task_update_status: task %s is assigned to %s, not %s", tk.ID, tk.WorkerID, actor.ID)
	}
	next, err := taskToolStatus(in.Status)
	if err != nil {
		return "", err
	}
	tk, err = t.a.tasks.Update(taskID, taskpkg.UpdateTaskInput{
		Status:  next,
		Outcome: strings.TrimSpace(in.Outcome),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("task status updated: %s status=%s", tk.ID, tk.Status), nil
}

func (a *App) requireTaskCapability(ctx context.Context, caps ...string) (*persona.Persona, error) {
	id := strings.TrimSpace(command.PersonaFrom(ctx))
	if id == "" {
		return nil, fmt.Errorf("task tool requires a persona identity")
	}
	p := a.personas.Get(id)
	if p == nil {
		return nil, fmt.Errorf("task tool persona not found: %s", id)
	}
	for _, cap := range caps {
		if p.HasCapability(cap) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("persona %s lacks required capability: %s", p.ID, strings.Join(caps, " or "))
}

func (a *App) requireTaskStatusCapability(ctx context.Context, status string) (*persona.Persona, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "finished", "closed", "close", "canceled", "cancelled":
		return a.requireTaskCapability(ctx, capTaskReview)
	default:
		return a.requireTaskCapability(ctx, capTaskExecute)
	}
}

func (a *App) taskAssignee(id string) (*persona.Persona, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("task assignee is required")
	}
	p := a.personas.Get(id)
	if p == nil {
		return nil, fmt.Errorf("task assignee not found: %s", id)
	}
	if !p.HasCapability(capTaskExecute) {
		return nil, fmt.Errorf("task assignee %s lacks required capability: %s", p.ID, capTaskExecute)
	}
	return p, nil
}

func (a *App) taskSpaceID(ctx context.Context, explicit string) (string, error) {
	if id := strings.TrimSpace(explicit); id != "" {
		return id, nil
	}
	src := command.SourceFrom(ctx)
	target := space.MapSource(src)
	if target.Kind == "" || strings.TrimSpace(target.Seed) == "" {
		return "", fmt.Errorf("task_create requires space_id for source %q", src)
	}
	if space.IsSpaceID(target.Seed) {
		if sp, err := a.spaces.LoadSpace(target.Seed); err == nil && sp != nil && sp.Kind == target.Kind {
			return sp.ID, nil
		}
	}
	sp, err := a.spaces.Store().FindSpaceByKindAndSeed(target.Kind, target.Seed)
	if err != nil {
		return "", err
	}
	if sp == nil {
		return "", fmt.Errorf("task_create requires space_id; current source %q has no existing space", src)
	}
	return sp.ID, nil
}

func taskToolStatus(status string) (taskpkg.Status, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "todo", "queued":
		return taskpkg.StatusQueued, nil
	case "doing", "running", "in_progress":
		return taskpkg.StatusRunning, nil
	case "review", "in_review", "in-review":
		return taskpkg.Status("in_review"), nil
	case "done", "finished":
		return taskpkg.StatusFinished, nil
	case "closed", "close", "canceled", "cancelled":
		return taskpkg.StatusCanceled, nil
	default:
		return "", fmt.Errorf("unsupported task status: %s", status)
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}
