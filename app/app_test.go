package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

func TestHandleInputUsesConfiguredRuntimeWithoutProvider(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
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

	out, err := a.HandleInput(context.Background(), "test", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Fatalf("reply = %q, want %q", out, "ok")
	}
}

func TestVisionRoutedImagesPersistInSpaceButNotSessionCache(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime: "stub",
		Models: map[string]config.ModelConfig{
			"vision": {Provider: "openai", Model: "vision"},
		},
		Vision:    "vision",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input, Attachments: turn.Attachments})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "saw image"})
			return nil
		}), nil
	})

	attachment := msg.Attachment{
		Kind: "image",
		Name: "photo.png",
		MIME: "image/png",
		Data: "abcd",
	}
	out, err := a.HandleInputWithAttachments(context.Background(), "desktop", "describe", []msg.Attachment{attachment})
	if err != nil {
		t.Fatal(err)
	}
	if out != "saw image" {
		t.Fatalf("reply = %q, want saw image", out)
	}

	if sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindDirectChat, "Sumi"); err != nil {
		t.Fatal(err)
	} else if sp != nil {
		t.Fatalf("root desktop vision send should not persist hidden Sumi direct, got %#v", sp)
	}

	s, err := a.CurrentSession("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("session messages = %#v, want user + assistant", s.Messages)
	}
	for _, m := range s.Messages {
		if len(m.Attachments) != 0 {
			t.Fatalf("session cache retained vision image attachment: %#v", s.Messages)
		}
	}
}

func TestFileCommandAttachesTextToCurrentSession(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("hello file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.HandleInput(context.Background(), "cli", "/file note.md"); err != nil {
		t.Fatal(err)
	}
	s, err := a.CurrentSession("cli")
	if err != nil {
		t.Fatal(err)
	}
	last := s.Messages[len(s.Messages)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "hello file") {
		t.Fatalf("last message = %#v", last)
	}
}

func TestProjectContextIsPassedToRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sumi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sumi", "context.md"), []byte("Use tiny functions."), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	var got string
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		got = env.ProjectContext
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})
	if _, err := a.HandleInput(context.Background(), "test", "ping"); err != nil {
		t.Fatal(err)
	}
	if got != "Use tiny functions." {
		t.Fatalf("project context = %q", got)
	}
}

func TestHandleInputBuildsRunContext(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var got command.RunContext
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			var ok bool
			got, ok = command.RunContextFrom(ctx)
			if !ok {
				t.Fatal("missing run context")
			}
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})

	ctx := command.WithNoticeSource(context.Background(), "tg:dm:42")
	if _, err := a.HandleInput(ctx, "cron:bazaar", "ping"); err != nil {
		t.Fatal(err)
	}
	if got.Source != "cron:bazaar" || got.Session != "cron:bazaar" || got.Delivery != "tg:dm:42" {
		t.Fatalf("run context = %+v", got)
	}
	if got.Permission != "cron" {
		t.Fatalf("permission = %q", got.Permission)
	}
	if len(got.Memory) != 4 {
		t.Fatalf("memory scopes = %+v", got.Memory)
	}
	if got.Memory[0].Kind != "channel" || got.Memory[0].Key != "cron:bazaar" {
		t.Fatalf("first memory scope = %+v", got.Memory[0])
	}
	if got.Memory[1].Kind != "channel" || got.Memory[1].Key != "tg:dm:42" {
		t.Fatalf("second memory scope = %+v", got.Memory[1])
	}
}

func TestUsageCommandAggregatesRecordedUsage(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	s, err := a.sessions.New("cli")
	if err != nil {
		t.Fatal(err)
	}
	s.Add(msg.Message{Role: "assistant", Content: "ok", Usage: &msg.TokenUsage{Input: 10, Output: 5, Total: 15}})
	if s.Usage.Total != 15 {
		t.Fatalf("session usage = %+v", s.Usage)
	}
	if err := a.sessions.Save(s); err != nil {
		t.Fatal(err)
	}

	out, err := a.HandleInput(context.Background(), "cli", "/usage")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Current session", "input tokens: 10", "output tokens: 5", "total tokens: 15"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage output missing %q:\n%s", want, out)
		}
	}
}

func TestHandleInputReturnsLatestAssistant(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
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
		DataDir:   filepath.Join(dir, "sumi-data"),
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

func TestHandleInputPublishesCommandHandledForShellShortcut(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "native",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	events, cancel := a.Bus().Subscribe(8)
	defer cancel()

	if _, err := a.HandleInput(context.Background(), "test", "!printf hello"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type == bus.CommandHandled {
				if ev.Source != "test" || ev.Text != "hello" || ev.Err != "" {
					t.Fatalf("command event = %+v", ev)
				}
				return
			}
		case <-deadline:
			t.Fatal("missing command handled event")
		}
	}
}

func TestModelCommandSwitchesConfiguredAlias(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:     "stub",
		DataDir:     filepath.Join(dir, "sumi-data"),
		Workspace:   dir,
		ActiveModel: "main",
		Models: map[string]config.ModelConfig{
			"main": {
				Provider: "openai",
				Model:    "gpt-test",
				APIKey:   "test-key",
			},
			"deepseek_plat": {
				Provider: "openai",
				Model:    "deepseek-v4-flash",
				APIKey:   "test-key",
				BaseURL:  "https://example.test/v1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	out, err := a.HandleInput(context.Background(), "test", "/model deepseek_plat deepseek-v4-flash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "switched model to openai / deepseek-v4-flash") {
		t.Fatalf("out = %q", out)
	}
	if a.cfg.ActiveModel != "deepseek_plat" {
		t.Fatalf("active model = %q, want deepseek_plat", a.cfg.ActiveModel)
	}
	if a.cfg.BaseURL != "https://example.test/v1" {
		t.Fatalf("base url = %q", a.cfg.BaseURL)
	}
}

func TestModelCommandUnknownReturnsReadableMessage(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
		Models: map[string]config.ModelConfig{
			"main": {
				Provider: "openai",
				Model:    "gpt-test",
				APIKey:   "test-key",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	out, err := a.HandleInput(context.Background(), "test", "/model nope")
	if err != nil {
		t.Fatalf("model command should render a readable command response, got error: %v", err)
	}
	for _, want := range []string{"model switch failed", "model \"nope\" is not configured", "Use `/models`"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out missing %q: %q", want, out)
		}
	}
}

func TestUnknownSlashCommandReturnsDiscoverableMessage(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	out, err := a.HandleInput(context.Background(), "test", "/doesnotexist")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"unknown command: /doesnotexist", "Supported inline commands:", "/model", "/help"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out missing %q: %q", want, out)
		}
	}
}

func TestHandleInputAutoCompactsNativeRuntimeByModelWindow(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:     "native",
		DataDir:     filepath.Join(dir, "sumi-data"),
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
		DataDir:     filepath.Join(dir, "sumi-data"),
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
		DataDir:   filepath.Join(dir, "sumi-data"),
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
		if env.PreferencesPath != filepath.Join(dir, "sumi-data", "self", "preferences.md") {
			t.Fatalf("PreferencesPath = %q", env.PreferencesPath)
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

func TestHandleInputPersistsSessionWhenRuntimeFails(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "sumi-data")
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   dataDir,
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "partial"})
			return errors.New("boom")
		}), nil
	})

	if _, err := a.HandleInput(context.Background(), "test", "ping"); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}

	a2, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   dataDir,
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a2.Close() })

	s, err := a2.CurrentSession("test")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(s.Messages))
	}
	if s.Messages[0].Role != "assistant" || s.Messages[0].Content != "partial" {
		t.Fatalf("message = %#v", s.Messages[0])
	}

	evs, err := a2.ReplaySession(s.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("expected replay events for failed turn")
	}
	if evs[len(evs)-1].Type != bus.SessionUpdated {
		t.Fatalf("last event = %q, want %q", evs[len(evs)-1].Type, bus.SessionUpdated)
	}
	found := false
	for _, ev := range evs {
		if ev.Type == bus.TurnError && ev.Err == "boom" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing turn.error event: %#v", evs)
	}
}

type runtimeFunc func(context.Context, *agent.Turn) error

func (f runtimeFunc) Run(ctx context.Context, turn *agent.Turn) error {
	return f(ctx, turn)
}

func TestMentionReroutesToPersona(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("debug", persona.Meta{Runtime: "stub", Description: "bug hunter"}, "# Debug SOUL"); err != nil {
		t.Fatal(err)
	}

	var gotInput string
	var gotPersona *agent.Persona
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		gotPersona = env.Persona
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			gotInput = turn.Input
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})

	out, err := a.HandleInput(context.Background(), "test", "@debug 看看这个报错")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Fatalf("out = %q", out)
	}
	if gotInput != "看看这个报错" {
		t.Fatalf("input = %q, want stripped", gotInput)
	}
	if gotPersona == nil || gotPersona.ID != "debug" {
		t.Fatalf("persona = %#v, want debug", gotPersona)
	}
	main, err := a.CurrentSession("test")
	if err != nil {
		t.Fatal(err)
	}
	if len(main.Messages) != 0 {
		t.Fatalf("main session messages = %d, want 0", len(main.Messages))
	}
	ps, err := a.CurrentSession(personaSessionSource("test", "debug"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Messages) != 1 {
		t.Fatalf("persona session messages = %d, want 1", len(ps.Messages))
	}
}

func TestPersonaMentionKeepsSeparateThreadedContext(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("debug", persona.Meta{Runtime: "stub"}, "# Debug SOUL"); err != nil {
		t.Fatal(err)
	}

	var turns []struct {
		persona string
		input   string
		seen    int
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		id := ""
		if env.Persona != nil {
			id = env.Persona.ID
		}
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turns = append(turns, struct {
				persona string
				input   string
				seen    int
			}{persona: id, input: turn.Input, seen: len(turn.Session.Messages)})
			turn.Session.Add(msg.Message{Role: "assistant", Content: id + ":" + turn.Input})
			return nil
		}), nil
	})

	if _, err := a.HandleInput(context.Background(), "test", "@debug first"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleInput(context.Background(), "test", "plain reply"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleInput(context.Background(), "test", "@debug second"); err != nil {
		t.Fatal(err)
	}

	if len(turns) != 3 {
		t.Fatalf("turns = %#v", turns)
	}
	if turns[0].persona != "debug" || turns[0].input != "first" || turns[0].seen != 0 {
		t.Fatalf("first turn = %#v", turns[0])
	}
	if turns[1].persona != "" || turns[1].input != "plain reply" || turns[1].seen != 0 {
		t.Fatalf("plain turn = %#v", turns[1])
	}
	if turns[2].persona != "debug" || turns[2].input != "second" || turns[2].seen == 0 {
		t.Fatalf("second persona turn = %#v", turns[2])
	}
}

func TestHandleInputAsMissingPersona(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error { return nil }), nil
	})

	if _, err := a.HandleInputAs(context.Background(), "cli", "ghost", "hi"); err == nil {
		t.Fatal("expected error for missing persona")
	}
}

func TestPersonaEnvWiresSoulAndRuntime(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "fallback",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("reviewer", persona.Meta{Runtime: "stub"}, "stay critical"); err != nil {
		t.Fatal(err)
	}

	var env *agent.RuntimeEnv
	a.RegisterRuntime("stub", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		env = e
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})
	a.RegisterRuntime("fallback", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatalf("fallback runtime should not be invoked for persona with explicit runtime")
		return nil, nil
	})

	if _, err := a.HandleInputAs(context.Background(), "cli", "reviewer", "hi"); err != nil {
		t.Fatal(err)
	}
	if env == nil || env.Persona == nil {
		t.Fatal("persona env not populated")
	}
	if env.Persona.ID != "reviewer" {
		t.Fatalf("persona id = %q", env.Persona.ID)
	}
	if env.Persona.SoulPath == "" {
		t.Fatal("persona SoulPath empty")
	}
}

func TestAgentDMDoesNotRequireMention(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "fallback",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("helper", persona.Meta{Runtime: "stub"}, "help directly"); err != nil {
		t.Fatal(err)
	}

	var gotInput string
	var gotPersona *agent.Persona
	var turns []struct {
		input          string
		seen           []msg.Message
		includeHistory bool
		disableResume  bool
	}
	a.RegisterRuntime("stub", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		gotPersona = e.Persona
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			gotInput = turn.Input
			turns = append(turns, struct {
				input          string
				seen           []msg.Message
				includeHistory bool
				disableResume  bool
			}{
				input:          turn.Input,
				seen:           append([]msg.Message(nil), turn.Session.Messages...),
				includeHistory: turn.IncludeHistory,
				disableResume:  turn.DisableExternalResume,
			})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "direct ok: " + turn.Input})
			return nil
		}), nil
	})
	a.RegisterRuntime("fallback", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("fallback runtime should not handle agent dm")
		return nil, nil
	})

	out, err := a.HandleInput(context.Background(), "desktop:agent:helper", "hello without mention")
	if err != nil {
		t.Fatal(err)
	}
	if out != "direct ok: hello without mention" {
		t.Fatalf("out = %q, want direct ok", out)
	}
	if gotInput != "hello without mention" {
		t.Fatalf("input = %q, want original text", gotInput)
	}
	if gotPersona == nil || gotPersona.ID != "helper" {
		t.Fatalf("persona = %#v, want helper", gotPersona)
	}
	sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindAgentDM, "helper")
	if err != nil || sp == nil {
		t.Fatalf("agent dm space not found: %v", err)
	}
	if len(sp.Messages) != 2 {
		t.Fatalf("agent dm messages = %d, want user + assistant", len(sp.Messages))
	}
	out, err = a.HandleInput(context.Background(), "desktop:agent:helper", "second question")
	if err != nil {
		t.Fatal(err)
	}
	if out != "direct ok: second question" {
		t.Fatalf("second out = %q, want direct ok", out)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(turns))
	}
	if !turns[0].includeHistory || !turns[1].includeHistory {
		t.Fatalf("IncludeHistory = %v / %v, want true", turns[0].includeHistory, turns[1].includeHistory)
	}
	if !turns[0].disableResume || !turns[1].disableResume {
		t.Fatalf("DisableExternalResume = %v / %v, want true", turns[0].disableResume, turns[1].disableResume)
	}
	if len(turns[0].seen) != 0 {
		t.Fatalf("first seeded context = %d, want empty", len(turns[0].seen))
	}
	if len(turns[1].seen) < 2 {
		t.Fatalf("second seeded context too small: %+v", turns[1].seen)
	}
	if turns[1].seen[0].Role != "user" || turns[1].seen[0].Content != "[user] hello without mention" {
		t.Fatalf("second first context = %+v", turns[1].seen[0])
	}
	if turns[1].seen[1].Role != "assistant" || turns[1].seen[1].Content != "direct ok: hello without mention" {
		t.Fatalf("second assistant context = %+v", turns[1].seen[1])
	}
}

func TestDesktopDefaultSumiDoesNotBindDefaultPersona(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:        "stub",
		DataDir:        filepath.Join(dir, "sumi-data"),
		Workspace:      dir,
		DefaultPersona: "andy",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("andy", persona.Meta{Display: "Andy", Runtime: "andy-rt"}, "default persona"); err != nil {
		t.Fatal(err)
	}

	var gotPersona *agent.Persona
	var gotAgentID string
	a.RegisterRuntime("stub", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		gotPersona = e.Persona
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			gotAgentID = turn.AgentID
			turn.Session.Add(msg.Message{Role: "assistant", Content: "sumi ok"})
			return nil
		}), nil
	})
	a.RegisterRuntime("andy-rt", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("default persona runtime should not handle default Sumi conversation")
		return nil, nil
	})

	out, err := a.HandleInput(context.Background(), "desktop", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "sumi ok" {
		t.Fatalf("out = %q, want sumi ok", out)
	}
	if gotPersona != nil {
		t.Fatalf("runtime persona = %#v, want nil", gotPersona)
	}
	if gotAgentID != "" {
		t.Fatalf("turn agent id = %q, want empty default Sumi agent id", gotAgentID)
	}
	if sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindDirectChat, "Sumi"); err != nil {
		t.Fatalf("find default Sumi space: %v", err)
	} else if sp != nil {
		t.Fatalf("root desktop should not persist hidden Sumi direct, got %#v", sp)
	}
	if sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindAgentDM, "andy"); err != nil || sp != nil {
		t.Fatalf("default persona agent dm should not be created, got space=%#v err=%v", sp, err)
	}
}

func TestCLIDefaultSumiDoesNotBindDefaultPersona(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:        "stub",
		DataDir:        filepath.Join(dir, "sumi-data"),
		Workspace:      dir,
		DefaultPersona: "andy",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("andy", persona.Meta{Display: "Andy", Runtime: "andy-rt"}, "default persona"); err != nil {
		t.Fatal(err)
	}

	var gotPersona *agent.Persona
	var gotAgentID string
	a.RegisterRuntime("stub", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		gotPersona = e.Persona
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			gotAgentID = turn.AgentID
			turn.Session.Add(msg.Message{Role: "assistant", Content: "sumi ok"})
			return nil
		}), nil
	})
	a.RegisterRuntime("andy-rt", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("default persona runtime should not handle default CLI conversation")
		return nil, nil
	})

	out, err := a.HandleInput(context.Background(), "cli", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "sumi ok" {
		t.Fatalf("out = %q, want sumi ok", out)
	}
	if gotPersona != nil {
		t.Fatalf("runtime persona = %#v, want nil", gotPersona)
	}
	if gotAgentID != "" {
		t.Fatalf("turn agent id = %q, want empty default Sumi agent id", gotAgentID)
	}
	if sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindDirectChat, "cli"); err != nil || sp == nil {
		t.Fatalf("default CLI space not found: %v", err)
	} else if len(sp.Messages) != 2 || sp.Messages[1].AuthorID != "assistant" {
		t.Fatalf("default Sumi messages = %#v", sp.Messages)
	}
	if sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindAgentDM, "andy"); err != nil || sp != nil {
		t.Fatalf("default persona agent dm should not be created, got space=%#v err=%v", sp, err)
	}
}

func TestAgentDMTreatsMentionsAsText(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "fallback",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("helper", persona.Meta{Runtime: "helper-rt"}, "help directly"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Personas().Create("bob", persona.Meta{Runtime: "bob-rt"}, "should not be routed"); err != nil {
		t.Fatal(err)
	}

	var gotInput string
	var gotPersona *agent.Persona
	a.RegisterRuntime("helper-rt", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		gotPersona = e.Persona
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			gotInput = turn.Input
			turn.Session.Add(msg.Message{Role: "assistant", Content: "helper ok"})
			return nil
		}), nil
	})
	a.RegisterRuntime("bob-rt", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("agent dm mention should not route to bob")
		return nil, nil
	})
	a.RegisterRuntime("fallback", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("fallback runtime should not handle agent dm")
		return nil, nil
	})

	out, err := a.HandleInput(context.Background(), "desktop:agent:helper", "@bob hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "helper ok" {
		t.Fatalf("out = %q, want helper ok", out)
	}
	if gotInput != "@bob hello" {
		t.Fatalf("input = %q, want @bob hello", gotInput)
	}
	if gotPersona == nil || gotPersona.ID != "helper" {
		t.Fatalf("persona = %#v, want helper", gotPersona)
	}
}

func TestAgentDMSeedsContextViewFromSpace(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "fallback",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("helper", persona.Meta{Runtime: "helper-rt"}, "help directly"); err != nil {
		t.Fatal(err)
	}

	turns := 0
	a.RegisterRuntime("helper-rt", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turns++
			if turns == 2 {
				var got []string
				for _, m := range turn.Session.Messages {
					got = append(got, m.Content)
				}
				joined := strings.Join(got, "\n")
				if !strings.Contains(joined, "[user] first") || !strings.Contains(joined, "agent reply 1") {
					t.Fatalf("context view = %+v", got)
				}
				if strings.Contains(joined, "second") {
					t.Fatalf("current input leaked into context view: %+v", got)
				}
			}
			turn.Session.Add(msg.Message{Role: "assistant", Content: fmt.Sprintf("agent reply %d", turns)})
			return nil
		}), nil
	})
	a.RegisterRuntime("fallback", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("fallback runtime should not handle agent dm")
		return nil, nil
	})

	if _, err := a.HandleInput(context.Background(), "desktop:agent:helper", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleInput(context.Background(), "desktop:agent:helper", "second"); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramDirectUsesDefaultPersonaWithoutMentionRouting(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:        "fallback",
		DataDir:        filepath.Join(dir, "sumi-data"),
		Workspace:      dir,
		DefaultPersona: "andy",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("andy", persona.Meta{Display: "Andy", Runtime: "andy-rt"}, "default tg agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Personas().Create("bob", persona.Meta{Display: "Bob", Runtime: "bob-rt"}, "should not be mention-routed"); err != nil {
		t.Fatal(err)
	}

	var gotInput, gotSource string
	var gotPersona *agent.Persona
	a.RegisterRuntime("andy-rt", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		gotPersona = e.Persona
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			gotInput = turn.Input
			gotSource = turn.Session.Source
			turn.Session.Add(msg.Message{Role: "assistant", Content: "andy ok"})
			return nil
		}), nil
	})
	a.RegisterRuntime("bob-rt", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("telegram @bob text should not route to Bob")
		return nil, nil
	})
	a.RegisterRuntime("fallback", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		t.Fatal("fallback runtime should not handle configured default persona")
		return nil, nil
	})

	out, err := a.HandleInput(context.Background(), "tg:dm:42", "@bob hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "andy ok" {
		t.Fatalf("out = %q, want andy ok", out)
	}
	if gotInput != "@bob hello" {
		t.Fatalf("input = %q, want @bob hello", gotInput)
	}
	if gotSource != "tg:dm:42" {
		t.Fatalf("session source = %q, want tg:dm:42", gotSource)
	}
	if gotPersona == nil || gotPersona.ID != "andy" {
		t.Fatalf("persona = %#v, want Andy", gotPersona)
	}
	all, err := a.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if strings.Contains(s.Source, ":persona:") {
			t.Fatalf("telegram direct session should not be persona-suffixed: %q", s.Source)
		}
	}
	sp, err := a.Spaces().Store().FindSpaceByKindAndSeed(space.KindDirectChat, "tg:dm:42")
	if err != nil || sp == nil {
		t.Fatalf("telegram space not found: %v", err)
	}
	if len(sp.Messages) != 2 {
		t.Fatalf("telegram space messages = %d, want user + assistant", len(sp.Messages))
	}
	if sp.Messages[0].Content != "@bob hello" {
		t.Fatalf("space user content = %q", sp.Messages[0].Content)
	}
	if sp.Messages[1].AuthorID != "andy" || sp.Messages[1].Content != "andy ok" {
		t.Fatalf("space assistant message = %#v", sp.Messages[1])
	}
}

func TestTelegramDirectSeedsContextViewFromSpace(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:        "stub",
		DataDir:        filepath.Join(dir, "sumi-data"),
		Workspace:      dir,
		DefaultPersona: "andy",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("andy", persona.Meta{Display: "Andy", Runtime: "stub"}, "default tg agent"); err != nil {
		t.Fatal(err)
	}
	turns := 0
	a.RegisterRuntime("stub", func(e *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turns++
			if turns == 2 {
				var got []string
				for _, m := range turn.Session.Messages {
					got = append(got, m.Content)
				}
				joined := strings.Join(got, "\n")
				if !strings.Contains(joined, "[user] first") || !strings.Contains(joined, "reply 1") {
					t.Fatalf("context view = %+v", got)
				}
				if strings.Contains(joined, "second") {
					t.Fatalf("current input leaked into context view: %+v", got)
				}
			}
			turn.Session.Add(msg.Message{Role: "assistant", Content: fmt.Sprintf("reply %d", turns)})
			return nil
		}), nil
	})

	if _, err := a.HandleInput(context.Background(), "tg:dm:42", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleInput(context.Background(), "tg:dm:42", "second"); err != nil {
		t.Fatal(err)
	}
}
