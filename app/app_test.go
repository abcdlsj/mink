package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/msg"
)

func TestHandleInputUsesConfiguredRuntimeWithoutProvider(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DBPath:    filepath.Join(dir, "mink.db"),
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

	out, err := a.HandleInput(context.Background(), "test", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Fatalf("reply = %q, want %q", out, "ok")
	}
}

func TestHandleInputReturnsLatestAssistant(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DBPath:    filepath.Join(dir, "mink.db"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "first"})
			turn.Session.Add(msg.Message{Role: "tool", ToolResults: []msg.ToolResult{{
				ToolCallID: "tool-1",
				Content:    "done",
			}}})
			return nil
		}), nil
	})

	out, err := a.HandleInput(context.Background(), "test", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if out != "first" {
		t.Fatalf("reply = %q, want %q", out, "first")
	}
}

func TestHandleInputRunsBangCommandAsShellShortcut(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "native",
		DBPath:    filepath.Join(dir, "mink.db"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	out, err := a.HandleInput(context.Background(), "test", "!printf hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("reply = %q, want %q", out, "hello")
	}
}

type runtimeFunc func(context.Context, *agent.Turn) error

func (f runtimeFunc) Run(ctx context.Context, turn *agent.Turn) error {
	return f(ctx, turn)
}
