package cron

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/command"

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
	ctx := command.WithNoticeSource(context.Background(), task.Source)
	out, err := s.app.HandleInput(ctx, cronSource(task), task.Prompt)
	if err != nil {
		s.app.PublishNotice(task.Source, fmt.Sprintf("[cron %s] error: %s", task.ID, err))
		return
	}
	s.app.PublishNotice(task.Source, out)
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
