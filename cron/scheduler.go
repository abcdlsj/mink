package cron

import (
	"context"
	"sync"

	"github.com/abcdlsj/mink/bus"
	robcron "github.com/robfig/cron/v3"
)

type Scheduler struct {
	path string
	bus  *bus.Bus
	c    *robcron.Cron
	mu   sync.Mutex
}

func NewScheduler(path string, b *bus.Bus) *Scheduler {
	return &Scheduler{
		path: path,
		bus:  b,
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.c = robcron.New()
	if err := s.loadLocked(); err != nil {
		return err
	}
	s.c.Start()

	go func() {
		<-ctx.Done()
		s.c.Stop()
	}()

	return nil
}

func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.c == nil {
		return nil
	}

	for _, e := range s.c.Entries() {
		s.c.Remove(e.ID)
	}
	return s.loadLocked()
}

func (s *Scheduler) loadLocked() error {
	tasks, err := LoadTasks(s.path)
	if err != nil {
		return err
	}

	for _, t := range tasks {
		if !t.Enabled {
			continue
		}
		task := t
		_, err := s.c.AddFunc(task.Schedule, func() {
			_ = s.bus.Pub(bus.Msg{
				Type:    bus.TypeCronTrigger,
				From:    task.Source,
				To:      bus.AddrAgentMain,
				Payload: task.Prompt,
			})
		})
		if err != nil {
			continue
		}
	}
	return nil
}
