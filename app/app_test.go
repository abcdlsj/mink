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
		DataDir:   filepath.Join(dir, "mink-data"),
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
		DataDir:   filepath.Join(dir, "mink-data"),
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
		DataDir:   filepath.Join(dir, "mink-data"),
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

func TestHandleInputAutoCompactsNativeRuntimeByModelWindow(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:     "native",
		DataDir:     filepath.Join(dir, "mink-data"),
		Workspace:   dir,
		MaxTokens:   1,
		ActiveModel: "main",
		Default:     "main",
		Models: map[string]config.ModelConfig{
			"main": {
				Provider:      "openai",
				Model:         "test",
				APIKey:        "test-key",
				MaxTokens:     1,
				ContextWindow: 20,
			},
		},
		Compact: config.CompactConfig{
			Auto:               true,
			KeepRecentMessages: 0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	for i := 0; i < 5; i++ {
		s, err := a.CurrentSession("test")
		if err != nil {
			t.Fatal(err)
		}
		s.Add(msg.Message{Role: "user", Content: "old message with enough content to trigger compact"})
		if err := a.SaveSession(s); err != nil {
			t.Fatal(err)
		}
	}

	a.RegisterRuntime("native", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			if turn.Session.Summary == "" {
				t.Fatalf("expected auto compact summary")
			}
			if len(turn.Session.Messages) != 1 {
				t.Fatalf("got %d messages before runtime, want 1", len(turn.Session.Messages))
			}
			if turn.Session.Messages[0].Role != "system" {
				t.Fatalf("expected summary system message, got %s", turn.Session.Messages[0].Role)
			}
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

func TestHandleInputDoesNotAutoCompactExternalDriverRuntime(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:     "codex",
		DataDir:     filepath.Join(dir, "mink-data"),
		Workspace:   dir,
		MaxTokens:   1,
		ActiveModel: "main",
		Default:     "main",
		Models: map[string]config.ModelConfig{
			"main": {
				Provider:      "openai",
				Model:         "test",
				APIKey:        "test-key",
				MaxTokens:     1,
				ContextWindow: 20,
			},
		},
		Compact: config.CompactConfig{
			Auto:               true,
			KeepRecentMessages: 0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	for i := 0; i < 5; i++ {
		s, err := a.CurrentSession("test")
		if err != nil {
			t.Fatal(err)
		}
		s.Add(msg.Message{Role: "user", Content: "old message for external driver"})
		if err := a.SaveSession(s); err != nil {
			t.Fatal(err)
		}
	}

	a.RegisterRuntime("codex", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			if turn.Session.Summary != "" {
				t.Fatalf("did not expect auto compact summary for external driver runtime")
			}
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

func TestHandleInputPassesPromptSettingsToRuntimeEnv(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "mink-data"),
		Workspace: dir,
		Prompt:    "项目约束",
		SoulPath:  filepath.Join(dir, "SOUL.md"),
		Telegram: config.TelegramConfig{
			MentionMode:  "smart",
			SessionScope: "thread",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		if env.Prompt != "项目约束" {
			t.Fatalf("Prompt = %q", env.Prompt)
		}
		if env.SoulPath != filepath.Join(dir, "SOUL.md") {
			t.Fatalf("SoulPath = %q", env.SoulPath)
		}
		if env.TelegramMentionMode != "smart" {
			t.Fatalf("TelegramMentionMode = %q", env.TelegramMentionMode)
		}
		if env.TelegramSessionScope != "thread" {
			t.Fatalf("TelegramSessionScope = %q", env.TelegramSessionScope)
		}
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})

	out, err := a.HandleInput(context.Background(), "telegram:1:2", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Fatalf("reply = %q, want %q", out, "ok")
	}
}

type runtimeFunc func(context.Context, *agent.Turn) error

func (f runtimeFunc) Run(ctx context.Context, turn *agent.Turn) error {
	return f(ctx, turn)
}
