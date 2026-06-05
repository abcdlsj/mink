package cron

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/command"
	taskpkg "github.com/abcdlsj/sumi/task"

	robcron "github.com/robfig/cron/v3"
)

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
	go s.stopOnDone(ctx)
	return nil
}

func (s *scheduler) stopOnDone(ctx context.Context) {
	<-ctx.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c == nil {
		return
	}
	s.c.Stop()
	s.c = nil
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
			s.run(task)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *scheduler) run(task Task) {
	taskID := s.createStateTask(task)
	runID := s.startStateTask(taskID, task)
	ctx := command.WithNoticeSource(context.Background(), task.Source)
	out, err := s.app.HandleInput(ctx, cronSource(task), task.Prompt)
	if err != nil {
		s.finishStateTask(taskID, runID, task, taskpkg.StatusFailed, "failed", nil, []string{err.Error()})
		s.app.PublishNotice(task.Source, fmt.Sprintf("[cron %s] error: %s", task.ID, err))
		return
	}
	s.finishStateTask(taskID, runID, task, taskpkg.StatusFinished, "done", []string{task.Source}, nil)
	s.app.PublishNotice(task.Source, out)
}

func (s *scheduler) createStateTask(task Task) string {
	if s == nil || s.app == nil || s.app.Tasks() == nil {
		return ""
	}
	state := taskpkg.TaskState{
		Goal:       strings.TrimSpace(task.Prompt),
		Todo:       []string{"run scheduled prompt", "publish result notice"},
		Checkpoint: "queued",
		RelatedIDs: []string{task.ID, task.Source},
	}
	tk, err := s.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          cronSource(task),
		TriggerMessageID: task.ID,
		InitiatorID:      "cron",
		WorkerID:         "cron",
		Title:            cronTitle(task),
		Source:           task.Source,
		State:            state,
	})
	if err != nil {
		return ""
	}
	return tk.ID
}

func (s *scheduler) startStateTask(taskID string, task Task) string {
	if s == nil || s.app == nil || s.app.Tasks() == nil || taskID == "" {
		return ""
	}
	state := taskpkg.TaskState{
		Goal:       strings.TrimSpace(task.Prompt),
		Todo:       []string{"publish result notice"},
		Checkpoint: "running",
		RelatedIDs: []string{task.ID, task.Source},
	}
	run, err := s.app.Tasks().StartRun(taskID, state)
	if err != nil {
		return ""
	}
	_, _ = s.app.Tasks().Update(taskID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning, State: &state})
	return run.ID
}

func (s *scheduler) finishStateTask(taskID, runID string, task Task, status taskpkg.Status, checkpoint string, artifacts, blockers []string) {
	if s == nil || s.app == nil || s.app.Tasks() == nil || taskID == "" {
		return
	}
	state := taskpkg.TaskState{
		Goal:       strings.TrimSpace(task.Prompt),
		Checkpoint: checkpoint,
		Artifacts:  artifacts,
		Blockers:   blockers,
		RelatedIDs: []string{task.ID, task.Source},
	}
	_, _ = s.app.Tasks().Update(taskID, taskpkg.UpdateTaskInput{Status: status, Outcome: checkpoint, State: &state})
	if runID != "" {
		_, _ = s.app.Tasks().FinishRun(runID, taskpkg.FinishRunInput{Status: status, State: &state})
	}
}

func cronTitle(task Task) string {
	title := strings.TrimSpace(task.Name)
	if title == "" {
		title = strings.TrimSpace(task.Prompt)
	}
	rs := []rune(title)
	if len(rs) > taskpkg.MaxTitleLen {
		title = string(rs[:taskpkg.MaxTitleLen])
	}
	return title
}

func cronSource(task Task) string {
	id := strings.TrimSpace(task.ID)
	if id == "" {
		return "cron"
	}
	return "cron:" + id
}

func validSchedule(v string) error {
	_, err := robcron.ParseStandard(v)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}

func newTaskID() string {
	return fmt.Sprintf("cron-%d", time.Now().UnixNano())
}
