package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
)

type appRuntimeFunc func(context.Context, *agent.Turn) error

func (f appRuntimeFunc) Run(ctx context.Context, turn *agent.Turn) error {
	return f(ctx, turn)
}

func TestExternalPersonaRememberWritesSumiPersonaMemory(t *testing.T) {
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
	if _, err := a.Personas().Create("bob", persona.Meta{Runtime: "claude"}, "# Bob"); err != nil {
		t.Fatal(err)
	}

	var notice, brief, systemPrompt string
	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return appRuntimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			notice = turn.MemoryNotice
			brief = turn.MemoryBrief
			systemPrompt = agent.BuildSystemPrompt(env, turn)
			turn.Session.Add(msg.Message{Role: "assistant", Content: "Remembered."})
			return nil
		}), nil
	})

	out, err := a.HandleInputAs(context.Background(), "desktop:channel:alpha", "bob", "记住我喜欢用中文简洁回答")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Remembered." {
		t.Fatalf("out = %q", out)
	}
	if !strings.Contains(notice, "Remembered memory") {
		t.Fatalf("memory notice = %q", notice)
	}
	if !strings.Contains(brief, "persona:bob") || !strings.Contains(brief, "中文简洁") {
		t.Fatalf("memory brief = %q", brief)
	}
	if strings.Contains(systemPrompt, ".claude") || strings.Contains(systemPrompt, "MEMORY.md") {
		t.Fatalf("system prompt leaked host memory path: %q", systemPrompt)
	}
	entries, err := os.ReadDir(filepath.Join(a.Config().MemoryDir(), "persona", "bob"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("persona memory files = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(a.Config().MemoryDir(), "persona", "bob", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`scope_kind: "persona"`,
		`scope_key: "bob"`,
		`source: "desktop:channel:alpha"`,
		`created_by: "user"`,
		"中文简洁",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("memory file missing %q:\n%s", want, text)
		}
	}
}

func TestExternalPersonaRememberViaSumiExposedWayPrecommits(t *testing.T) {
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
	if _, err := a.Personas().Create("bob", persona.Meta{Runtime: "codex"}, "# Bob"); err != nil {
		t.Fatal(err)
	}

	var notice, brief string
	a.RegisterRuntime("codex", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return appRuntimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			notice = turn.MemoryNotice
			brief = turn.MemoryBrief
			turn.Session.Add(msg.Message{Role: "assistant", Content: "已记住。"})
			return nil
		}), nil
	})

	out, err := a.HandleInputAs(context.Background(), "desktop:channel:alpha", "bob", "那你记忆一下吧，用 sumi 暴露给你的方式：我喜欢中文简洁回答")
	if err != nil {
		t.Fatal(err)
	}
	if out != "已记住。" {
		t.Fatalf("out = %q", out)
	}
	if !strings.Contains(notice, "Remembered memory") {
		t.Fatalf("memory notice = %q", notice)
	}
	if !strings.Contains(notice, "To undo: !memory delete") {
		t.Fatalf("memory notice missing undo path: %q", notice)
	}
	if !strings.Contains(brief, "persona:bob") || !strings.Contains(brief, "中文简洁") {
		t.Fatalf("memory brief = %q", brief)
	}
	entries, err := os.ReadDir(filepath.Join(a.Config().MemoryDir(), "persona", "bob"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("persona memory files = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(a.Config().MemoryDir(), "persona", "bob", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "User explicitly asked Sumi to remember: 我喜欢中文简洁回答") {
		t.Fatalf("memory file did not keep the remembered content clean:\n%s", text)
	}
	if strings.Contains(text, "暴露给你的方式") {
		t.Fatalf("memory file kept routing instruction noise:\n%s", text)
	}
}
