package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/command"
)

type toolImpl struct {
	s *scheduler
}

func (t *toolImpl) Name() string { return "cron" }

func (t *toolImpl) Desc() string {
	return "Manage scheduled prompts. Actions: add, list, update, remove, toggle."
}

func (t *toolImpl) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []string{"add", "list", "update", "remove", "toggle"}},
			"id":       map[string]any{"type": "string"},
			"name":     map[string]any{"type": "string"},
			"schedule": map[string]any{"type": "string"},
			"prompt":   map[string]any{"type": "string"},
			"source":   map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}
}

func (t *toolImpl) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in params
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("cron: parse error: %w", err)
	}
	switch strings.TrimSpace(in.Action) {
	case "add":
		return t.add(ctx, in)
	case "list":
		return t.list()
	case "update":
		return t.update(in)
	case "remove":
		return t.remove(in.ID)
	case "toggle":
		return t.toggle(in.ID)
	default:
		return "", fmt.Errorf("unknown action: %s", in.Action)
	}
}

func (t *toolImpl) add(ctx context.Context, in params) (string, error) {
	if strings.TrimSpace(in.Schedule) == "" || strings.TrimSpace(in.Prompt) == "" {
		return "", fmt.Errorf("schedule and prompt are required")
	}
	if err := validSchedule(in.Schedule); err != nil {
		return "", err
	}
	tasks, err := loadTasks(t.s.path)
	if err != nil {
		return "", err
	}
	task := Task{
		ID:       newTaskID(),
		Name:     strings.TrimSpace(in.Name),
		Schedule: strings.TrimSpace(in.Schedule),
		Prompt:   strings.TrimSpace(in.Prompt),
		Enabled:  true,
		Source:   defaultSource(command.SourceFrom(ctx), in.Source),
	}
	tasks = append(tasks, task)
	if err := saveTasks(t.s.path, tasks); err != nil {
		return "", err
	}
	if err := t.s.Reload(); err != nil {
		return "", err
	}
	return fmt.Sprintf("created task %s", task.ID), nil
}

func (t *toolImpl) list() (string, error) {
	tasks, err := loadTasks(t.s.path)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "No scheduled tasks.", nil
	}
	var sb strings.Builder
	for _, task := range tasks {
		status := "enabled"
		if !task.Enabled {
			status = "disabled"
		}
		fmt.Fprintf(&sb, "[%s] %s schedule=%s source=%s status=%s\n", task.ID, blank(task.Name, task.Prompt), task.Schedule, task.Source, status)
	}
	return strings.TrimSpace(sb.String()), nil
}

func (t *toolImpl) update(in params) (string, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if strings.TrimSpace(in.Schedule) != "" {
		if err := validSchedule(in.Schedule); err != nil {
			return "", err
		}
	}
	tasks, err := loadTasks(t.s.path)
	if err != nil {
		return "", err
	}
	for i := range tasks {
		if tasks[i].ID != id {
			continue
		}
		updateTask(&tasks[i], in)
		if err := saveTasks(t.s.path, tasks); err != nil {
			return "", err
		}
		if err := t.s.Reload(); err != nil {
			return "", err
		}
		return fmt.Sprintf("updated task %s", tasks[i].ID), nil
	}
	return "", fmt.Errorf("task not found: %s", in.ID)
}

func updateTask(task *Task, in params) {
	if v := strings.TrimSpace(in.Name); v != "" {
		task.Name = v
	}
	if v := strings.TrimSpace(in.Schedule); v != "" {
		task.Schedule = v
	}
	if v := strings.TrimSpace(in.Prompt); v != "" {
		task.Prompt = v
	}
	if v := strings.TrimSpace(in.Source); v != "" {
		task.Source = v
	}
}

func (t *toolImpl) remove(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	tasks, err := loadTasks(t.s.path)
	if err != nil {
		return "", err
	}
	out := tasks[:0]
	found := false
	for _, task := range tasks {
		if task.ID == id {
			found = true
			continue
		}
		out = append(out, task)
	}
	if !found {
		return "", fmt.Errorf("task not found: %s", id)
	}
	if err := saveTasks(t.s.path, out); err != nil {
		return "", err
	}
	if err := t.s.Reload(); err != nil {
		return "", err
	}
	return fmt.Sprintf("removed task %s", id), nil
}

func (t *toolImpl) toggle(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	tasks, err := loadTasks(t.s.path)
	if err != nil {
		return "", err
	}
	for i := range tasks {
		if tasks[i].ID != id {
			continue
		}
		tasks[i].Enabled = !tasks[i].Enabled
		if err := saveTasks(t.s.path, tasks); err != nil {
			return "", err
		}
		if err := t.s.Reload(); err != nil {
			return "", err
		}
		if tasks[i].Enabled {
			return fmt.Sprintf("task %s enabled", id), nil
		}
		return fmt.Sprintf("task %s disabled", id), nil
	}
	return "", fmt.Errorf("task not found: %s", id)
}

func defaultSource(ctxSource, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(ctxSource); v != "" {
		return v
	}
	return "cli"
}
