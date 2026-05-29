package sessioncmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
)

func TestReplayCommandUsesRunLog(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := Plugin()(a); err != nil {
		t.Fatal(err)
	}

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Bus.Publish(bus.Event{
				Type:       bus.ToolCallStarted,
				Source:     turn.Source,
				SessionID:  turn.Session.ID,
				ToolCallID: "tool-1",
				Tool:       "bash",
				Input:      `{"cmd":"printf hi"}`,
			})
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			turn.Bus.Publish(bus.Event{
				Type:       bus.ToolCallFinished,
				Source:     turn.Source,
				SessionID:  turn.Session.ID,
				ToolCallID: "tool-1",
				Tool:       "bash",
				Output:     "hi",
			})
			return nil
		}), nil
	})

	if _, err := a.HandleInput(context.Background(), "test", "ping"); err != nil {
		t.Fatal(err)
	}

	out, err := (&replayCmd{app: a}).Run(command.WithSource(context.Background(), "test"), []string{"10"})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"turn.started", "tool.call.started", "tool.call.finished", "session.updated", "turn.finished"} {
		if !strings.Contains(out, part) {
			t.Fatalf("replay output missing %q:\n%s", part, out)
		}
	}
}

func TestInspectCommandShowsSnapshotAndRunLog(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})

	if _, err := a.HandleInput(context.Background(), "test", "ping"); err != nil {
		t.Fatal(err)
	}

	out, err := (&inspectCmd{app: a}).Run(command.WithSource(context.Background(), "test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Session ", "snapshot:", "runlog:", "Recent events:", "session.saved"} {
		if !strings.Contains(out, part) {
			t.Fatalf("inspect output missing %q:\n%s", part, out)
		}
	}
}

type runtimeFunc func(context.Context, *agent.Turn) error

func (f runtimeFunc) Run(ctx context.Context, turn *agent.Turn) error {
	return f(ctx, turn)
}
