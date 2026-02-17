package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cron"
	robcron "github.com/robfig/cron/v3"
)

type Cron struct {
	path      string
	scheduler *cron.Scheduler
}

func NewCron(path string, scheduler *cron.Scheduler) *Cron {
	return &Cron{path: path, scheduler: scheduler}
}

func (c *Cron) Name() string { return "cron" }
func (c *Cron) Desc() string {
	return "Manage scheduled tasks. Actions: add, list, update, remove, toggle."
}

func (c *Cron) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []string{"add", "list", "update", "remove", "toggle"}, "description": "Action to perform"},
			"id":       map[string]any{"type": "string", "description": "Task ID (required for update/remove/toggle)"},
			"name":     map[string]any{"type": "string", "description": "Task name"},
			"schedule": map[string]any{"type": "string", "description": "Cron expression (e.g. '0 9 * * *')"},
			"prompt":   map[string]any{"type": "string", "description": "Prompt to execute"},
			"source":   map[string]any{"type": "string", "description": "Result destination (defaults to current source)"},
		},
		"required": []string{"action"},
	}
}

func (c *Cron) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action   string `json:"action"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		Source   string `json:"source"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}

	switch p.Action {
	case "add":
		return c.add(ctx, p.Name, p.Schedule, p.Prompt, p.Source)
	case "list":
		return c.list()
	case "update":
		return c.update(p.ID, p.Name, p.Schedule, p.Prompt, p.Source)
	case "remove":
		return c.remove(p.ID)
	case "toggle":
		return c.toggle(p.ID)
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func (c *Cron) add(ctx context.Context, name, schedule, prompt, source string) (string, error) {
	if schedule == "" || prompt == "" {
		return "", fmt.Errorf("schedule and prompt are required")
	}
	if _, err := robcron.ParseStandard(schedule); err != nil {
		return "", fmt.Errorf("invalid cron expression: %w", err)
	}
	if source == "" {
		source = bus.SourceFrom(ctx)
	}
	if source == "" {
		return "", fmt.Errorf("source is required")
	}

	tasks, err := cron.LoadTasks(c.path)
	if err != nil {
		return "", err
	}

	t := cron.Task{
		ID:       cron.NewID(),
		Name:     name,
		Schedule: schedule,
		Prompt:   prompt,
		Enabled:  true,
		Source:   source,
	}
	tasks = append(tasks, t)

	if err := cron.SaveTasks(c.path, tasks); err != nil {
		return "", err
	}
	if err := c.scheduler.Reload(); err != nil {
		return "", fmt.Errorf("saved but reload failed: %w", err)
	}

	return fmt.Sprintf("Created task %s (%s) schedule=%s", t.ID, t.Name, t.Schedule), nil
}

func (c *Cron) list() (string, error) {
	tasks, err := cron.LoadTasks(c.path)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "No scheduled tasks.", nil
	}

	var b strings.Builder
	for _, t := range tasks {
		status := "enabled"
		if !t.Enabled {
			status = "disabled"
		}
		next := ""
		if t.Enabled {
			if sched, err := robcron.ParseStandard(t.Schedule); err == nil {
				next = " next=" + sched.Next(time.Now()).Format("2006-01-02 15:04")
			}
		}
		fmt.Fprintf(&b, "[%s] %s (%s) schedule=%s status=%s source=%s%s\n",
			t.ID, t.Name, t.Prompt, t.Schedule, status, t.Source, next)
	}
	return b.String(), nil
}

func (c *Cron) update(id, name, schedule, prompt, source string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if schedule != "" {
		if _, err := robcron.ParseStandard(schedule); err != nil {
			return "", fmt.Errorf("invalid cron expression: %w", err)
		}
	}

	tasks, err := cron.LoadTasks(c.path)
	if err != nil {
		return "", err
	}

	var t *cron.Task
	for i := range tasks {
		if tasks[i].ID == id {
			t = &tasks[i]
			break
		}
	}
	if t == nil {
		return "", fmt.Errorf("task %s not found", id)
	}

	if name != "" {
		t.Name = name
	}
	if schedule != "" {
		t.Schedule = schedule
	}
	if prompt != "" {
		t.Prompt = prompt
	}
	if source != "" {
		t.Source = source
	}

	if err := cron.SaveTasks(c.path, tasks); err != nil {
		return "", err
	}
	if err := c.scheduler.Reload(); err != nil {
		return "", fmt.Errorf("saved but reload failed: %w", err)
	}
	return fmt.Sprintf("Updated task %s (%s) schedule=%s", t.ID, t.Name, t.Schedule), nil
}

func (c *Cron) remove(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	tasks, err := cron.LoadTasks(c.path)
	if err != nil {
		return "", err
	}

	found := false
	filtered := tasks[:0]
	for _, t := range tasks {
		if t.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		return "", fmt.Errorf("task %s not found", id)
	}

	if err := cron.SaveTasks(c.path, filtered); err != nil {
		return "", err
	}
	if err := c.scheduler.Reload(); err != nil {
		return "", fmt.Errorf("saved but reload failed: %w", err)
	}
	return fmt.Sprintf("Removed task %s", id), nil
}

func (c *Cron) toggle(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	tasks, err := cron.LoadTasks(c.path)
	if err != nil {
		return "", err
	}

	found := false
	var status string
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Enabled = !tasks[i].Enabled
			if tasks[i].Enabled {
				status = "enabled"
			} else {
				status = "disabled"
			}
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("task %s not found", id)
	}

	if err := cron.SaveTasks(c.path, tasks); err != nil {
		return "", err
	}
	if err := c.scheduler.Reload(); err != nil {
		return "", fmt.Errorf("saved but reload failed: %w", err)
	}
	return fmt.Sprintf("Task %s is now %s", id, status), nil
}
