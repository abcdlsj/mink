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

func taskToolBlocks(p *persona.Persona) map[string]string {
	if p != nil && p.TaskPolicy == "auto_commit" {
		return nil
	}
	reason := "current task policy is propose-only; real Task Board changes require human confirmation"
	return map[string]string{
		"task_assign":        reason,
		"task_update_status": reason,
	}
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
		tool.Prop("authorization_text", "string", "Exact current user text that explicitly asks to create, record, or assign a task"),
		tool.Required("title", "assignee_id", "expected_outcome", "acceptance_criteria", "authorization_text"),
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
		AuthorizationText  string `json:"authorization_text"`
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
	if err := validateTaskCommitment(title, expected, criteria); err != nil {
		return "", err
	}
	if err := validateExplicitTaskAuthorization(ctx, in.AuthorizationText); err != nil {
		return "", err
	}
	sourceMessageID := firstNonEmpty(in.SourceMessageID, in.SourceMessage)
	sourceThreadID := firstNonEmpty(in.SourceThreadID, in.SourceThread, command.ParentMessageFrom(ctx))
	if sourceMessageID == "" {
		sourceMessageID = sourceThreadID
	}
	if persona := t.a.personas.Get(actor.ID); persona != nil && persona.TaskPolicy != "auto_commit" {
		proposal, err := t.a.PrepareTaskCreateProposal(ctx, TaskCreateProposalPayload{
			SpaceID:            spaceID,
			SourceMessageID:    sourceMessageID,
			SourceThreadID:     sourceThreadID,
			CreatedBy:          actor.ID,
			AssignedBy:         actor.ID,
			AssigneeID:         assignee.ID,
			Title:              title,
			ExpectedOutcome:    expected,
			AcceptanceCriteria: criteria,
			AuthorizationText:  in.AuthorizationText,
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("task proposal prepared: %s assigned_to=%s status=%s", proposal.ID, assignee.ID, proposal.Status), nil
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

func validateExplicitTaskAuthorization(ctx context.Context, authorizationText string) error {
	auth := strings.TrimSpace(authorizationText)
	current := strings.TrimSpace(command.InputFrom(ctx))
	if auth == "" {
		return fmt.Errorf("task_create requires authorization_text copied from the current user message")
	}
	if current != "" && !strings.Contains(current, auth) {
		return fmt.Errorf("authorization_text must be copied from the current user message")
	}
	if !explicitTaskAuthorization(auth) {
		return fmt.Errorf("task_create requires the current user to explicitly ask to create, record, or assign a task")
	}
	return nil
}

func explicitTaskAuthorization(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	compact := strings.Join(strings.Fields(s), " ")
	patterns := []string{
		"创建任务", "创建一个任务", "创建个任务", "新建任务", "新建一个任务", "新建个任务",
		"建个任务", "建一个任务", "加个任务", "加一个任务",
		"记为任务", "记录成任务", "转成任务", "创建 task", "新建 task", "create task",
		"make a task", "add a task", "assign task", "assign this", "作为任务",
	}
	for _, p := range patterns {
		if strings.Contains(compact, p) {
			return true
		}
	}
	return false
}

func validateTaskCommitment(title, expected, criteria string) error {
	title = strings.TrimSpace(title)
	expected = strings.TrimSpace(expected)
	criteria = strings.TrimSpace(criteria)
	if title == "" || expected == "" || criteria == "" {
		return fmt.Errorf("task_create requires title, expected_outcome, and acceptance_criteria")
	}
	if vagueTaskField(title) || vagueTaskField(expected) || vagueTaskField(criteria) {
		return fmt.Errorf("task_create requires a deliverable with concrete expected_outcome and acceptance_criteria; simple Q&A, one-off lookup, or vague TODO should stay as conversation")
	}
	if runeLen(expected) < 12 || runeLen(criteria) < 12 {
		return fmt.Errorf("task_create requires concrete expected_outcome and acceptance_criteria, not a short note")
	}
	return nil
}

func vagueTaskField(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return true
	}
	s = strings.Join(strings.Fields(s), " ")
	switch s {
	case "done", "finish", "finished", "complete", "completed", "ok", "fixed", "reviewed",
		"处理", "处理完", "完成", "搞定", "看一下", "查一下", "解释一下", "看看":
		return true
	default:
		return false
	}
}

func runeLen(s string) int {
	return len([]rune(s))
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
	return a.personaWithAnyCapability(
		id,
		caps,
		func(id string) error { return fmt.Errorf("task tool persona not found: %s", id) },
		func(p *persona.Persona, caps []string) error {
			return fmt.Errorf("persona %s lacks required capability: %s", p.ID, strings.Join(caps, " or "))
		},
	)
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
	return a.personaWithAnyCapability(
		id,
		[]string{capTaskExecute},
		func(id string) error { return fmt.Errorf("task assignee not found: %s", id) },
		func(p *persona.Persona, caps []string) error {
			return fmt.Errorf("task assignee %s lacks required capability: %s", p.ID, strings.Join(caps, " or "))
		},
	)
}

func (a *App) personaWithAnyCapability(id string, caps []string, notFound func(string) error, denied func(*persona.Persona, []string) error) (*persona.Persona, error) {
	id = strings.TrimSpace(id)
	p := a.personas.Get(id)
	if p == nil {
		return nil, notFound(id)
	}
	for _, cap := range caps {
		if p.HasCapability(cap) {
			return p, nil
		}
	}
	return nil, denied(p, caps)
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
