package cron

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
)

func TestSchedulerRunPublishesOutputNotice(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "喝水"})
			return nil
		}), nil
	})

	events, cancel := a.Bus().Subscribe(16)
	defer cancel()

	s := &scheduler{app: a}
	s.run(Task{ID: "cron-test", Source: "telegram:42", Prompt: "提醒喝水"})

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != bus.ServiceNotice {
				continue
			}
			if ev.Source != "telegram:42" || ev.Text != "喝水" {
				t.Fatalf("notice = %+v", ev)
			}
			return
		case <-deadline:
			t.Fatal("missing service notice")
		}
	}
}

type runtimeFunc func(context.Context, *agent.Turn) error

func (f runtimeFunc) Run(ctx context.Context, turn *agent.Turn) error {
	return f(ctx, turn)
}
