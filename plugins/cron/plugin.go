package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/command"
	robcron "github.com/robfig/cron/v3"
)

type Task struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
}

type params struct {
	Action   string `json:"action"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Source   string `json:"source"`
}

type scheduler struct {
	app  *app.App
	path string
	mu   sync.Mutex
	c    *robcron.Cron
}

func Plugin() app.Plugin {
	return func(a *app.App) error {
		s := &scheduler{
			app:  a,
			path: filepath.Join(filepath.Dir(a.Config().DBPath), "cron.json"),
		}
		a.RegisterTool(&toolImpl{s: s})
		a.RegisterService("cron", func(ctx context.Context, _ *app.App) error {
			return s.Start(ctx)
		})
		return nil
	}
}

func (s *scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		return nil
	}
	c := robcron.New()
	if err := s.load(c); err != nil {
		return err
	}
	s.c = c
	s.c.Start()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if s.c != nil {
			s.c.Stop()
			s.c = nil
		}
		s.mu.Unlock()
	}()
	return nil
}

func (s *scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c == nil {
		return nil
	}
	for _, e := range s.c.Entries() {
		s.c.Remove(e.ID)
	}
	return s.load(s.c)
}

func (s *scheduler) load(c *robcron.Cron) error {
	tasks, err := loadTasks(s.path)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		task := task
		if _, err := c.AddFunc(task.Schedule, func() {
			if _, err := s.app.HandleInput(context.Background(), task.Source, task.Prompt); err != nil {
				s.app.PublishNotice(task.Source, fmt.Sprintf("[cron %s] error: %s", task.ID, err))
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

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
	if _, err := robcron.ParseStandard(in.Schedule); err != nil {
		return "", fmt.Errorf("invalid cron expression: %w", err)
	}
	tasks, err := loadTasks(t.s.path)
	if err != nil {
		return "", err
	}
	src := strings.TrimSpace(in.Source)
	if src == "" {
		src = command.SourceFrom(ctx)
	}
	if src == "" {
		src = "cli"
	}
	task := Task{
		ID:       fmt.Sprintf("cron-%d", time.Now().UnixNano()),
		Name:     strings.TrimSpace(in.Name),
		Schedule: strings.TrimSpace(in.Schedule),
		Prompt:   strings.TrimSpace(in.Prompt),
		Enabled:  true,
		Source:   src,
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
	if strings.TrimSpace(in.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	if strings.TrimSpace(in.Schedule) != "" {
		if _, err := robcron.ParseStandard(in.Schedule); err != nil {
			return "", fmt.Errorf("invalid cron expression: %w", err)
		}
	}
	tasks, err := loadTasks(t.s.path)
	if err != nil {
		return "", err
	}
	for i := range tasks {
		if tasks[i].ID != strings.TrimSpace(in.ID) {
			continue
		}
		if strings.TrimSpace(in.Name) != "" {
			tasks[i].Name = strings.TrimSpace(in.Name)
		}
		if strings.TrimSpace(in.Schedule) != "" {
			tasks[i].Schedule = strings.TrimSpace(in.Schedule)
		}
		if strings.TrimSpace(in.Prompt) != "" {
			tasks[i].Prompt = strings.TrimSpace(in.Prompt)
		}
		if strings.TrimSpace(in.Source) != "" {
			tasks[i].Source = strings.TrimSpace(in.Source)
		}
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

func loadTasks(path string) ([]Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []Task
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func saveTasks(path string, tasks []Task) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}
