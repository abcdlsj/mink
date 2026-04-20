package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/msg"
)

func TestCLIShowsHeaderAndPrompt(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "claude",
		DBPath:    filepath.Join(dir, "mink.db"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	if err := runCLIWithIO(context.Background(), a, "cli", strings.NewReader("exit\n"), &out); err != nil {
		t.Fatal(err)
	}

	s, err := a.CurrentSession("cli")
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	head := "mink | runtime claude | model claude native | cwd " + shortPath(dir) + " | session " + s.ID
	if !strings.Contains(text, head) {
		t.Fatalf("output missing header %q:\n%s", head, text)
	}
	if !strings.Contains(text, "\n\n> ") {
		t.Fatalf("output missing prompt spacing:\n%s", text)
	}
	if strings.Contains(text, "mink v3") {
		t.Fatalf("output should not contain legacy banner:\n%s", text)
	}
	if strings.Contains(text, "\nruntime:") || strings.Contains(text, "\nmodel:") || strings.Contains(text, "\nsession:") {
		t.Fatalf("output should use a single-line header:\n%s", text)
	}
}

func TestCLIStreamsReplyOnceAndClosesLineBeforePrompt(t *testing.T) {
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
			turn.Bus.Publish(bus.Event{
				Type:      bus.TurnChunk,
				Source:    turn.Source,
				SessionID: turn.Session.ID,
				Text:      "hello",
			})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "hello"})
			return nil
		}), nil
	})

	var out bytes.Buffer
	if err := runCLIWithIO(context.Background(), a, "cli", strings.NewReader("ping\nexit\n"), &out); err != nil {
		t.Fatal(err)
	}

	text := out.String()
	if n := strings.Count(text, "hello"); n != 1 {
		t.Fatalf("hello count = %d, want 1:\n%s", n, text)
	}
	if !strings.Contains(text, "hello\n> ") {
		t.Fatalf("output should close the reply line before prompt:\n%s", text)
	}
}

func TestCLIFallsBackToReturnedReplyWhenNoChunksArrive(t *testing.T) {
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
			turn.Session.Add(msg.Message{Role: "assistant", Content: "done"})
			return nil
		}), nil
	})

	var out bytes.Buffer
	if err := runCLIWithIO(context.Background(), a, "cli", strings.NewReader("ping\nexit\n"), &out); err != nil {
		t.Fatal(err)
	}

	text := out.String()
	if n := strings.Count(text, "done"); n != 1 {
		t.Fatalf("done count = %d, want 1:\n%s", n, text)
	}
	if !strings.Contains(text, "done\n> ") {
		t.Fatalf("output should print returned reply before prompt:\n%s", text)
	}
}
