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

func TestExternalPersonaRememberRequiresAgentProposal(t *testing.T) {
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

	var notice, brief string
	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return appRuntimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			notice = turn.MemoryNotice
			brief = turn.MemoryBrief
			turn.Session.Add(msg.Message{Role: "assistant", Content: "我会整理成记忆候选。"})
			return nil
		}), nil
	})

	out, err := a.HandleInputAs(context.Background(), "desktop:channel:alpha", "bob", "记住我喜欢用中文简洁回答")
	if err != nil {
		t.Fatal(err)
	}
	if out != "我会整理成记忆候选。" {
		t.Fatalf("out = %q, want assistant response without precommit", out)
	}
	if notice != "" {
		t.Fatalf("memory notice = %q, want no user-text precommit", notice)
	}
	if strings.Contains(brief, "中文简洁") {
		t.Fatalf("memory brief got precommitted user text: %q", brief)
	}
	entries, err := os.ReadDir(filepath.Join(a.Config().MemoryDir(), "persona", "bob"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("persona memory files = %d, want 0", len(entries))
	}
}

func TestExternalPersonaMemoryProposalCommitsAndPersistsCard(t *testing.T) {
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

	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return appRuntimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "已整理。\n\n```sumi-memory\n{\"title\":\"Reply style\",\"body\":\"User prefers concise Chinese replies.\",\"kind\":\"preference\",\"reason\":\"User explicitly asked to remember this stable preference.\",\"confidence\":\"high\"}\n```"})
			return nil
		}), nil
	})

	out, err := a.HandleInputAs(context.Background(), "desktop:agent:bob", "bob", "记住我喜欢用中文简洁回答")
	if err != nil {
		t.Fatal(err)
	}
	if out != "已整理。" {
		t.Fatalf("out = %q, want fence stripped", out)
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
		`created_by: "bob"`,
		"User prefers concise Chinese replies.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("memory file missing %q:\n%s", want, text)
		}
	}

	spaces, err := a.Spaces().ListSpaces()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sp := range spaces {
		for _, m := range sp.Messages {
			if m.AuthorID != "bob" {
				continue
			}
			if strings.Contains(m.Content, "```sumi-memory") {
				t.Fatalf("space message leaked raw memory fence: %#v", m)
			}
			for _, att := range m.Attachments {
				if att.Kind == "memory_commit" && strings.Contains(att.Data, "Reply style") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("memory commit card attachment missing from Space")
	}
}

func TestExternalPersonaMemoryProposalAcceptsNumericConfidence(t *testing.T) {
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
	if _, err := a.Personas().Create("andy", persona.Meta{Runtime: "claude"}, "# Andy"); err != nil {
		t.Fatal(err)
	}

	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return appRuntimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "已整理。\n\n```sumi-memory\n{\"title\":\"Polymer app context\",\"body\":\"Bilibili polymer repos live under ~/Workspace/bili.\",\"kind\":\"fact\",\"reason\":\"User asked Sumi to remember durable project context.\",\"confidence\":0.95}\n```"})
			return nil
		}), nil
	})

	out, err := a.HandleInputAs(context.Background(), "desktop:agent:andy", "andy", "记住这些项目上下文")
	if err != nil {
		t.Fatal(err)
	}
	if out != "已整理。" {
		t.Fatalf("out = %q, want fence stripped", out)
	}
	entries, err := os.ReadDir(filepath.Join(a.Config().MemoryDir(), "persona", "andy"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("persona memory files = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(a.Config().MemoryDir(), "persona", "andy", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `confidence: "high"`) {
		t.Fatalf("numeric confidence was not normalized to high:\n%s", data)
	}
}

func TestExternalPersonaMemoryDeleteProposalDoesNotWriteMemory(t *testing.T) {
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
	if _, err := a.Personas().Create("andy", persona.Meta{Runtime: "claude"}, "# Andy"); err != nil {
		t.Fatal(err)
	}

	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return appRuntimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "试一下。\n\n```sumi-memory\n{\"title\":\"delete mistaken memory\",\"body\":\"Delete two mistaken memories.\",\"kind\":\"delete\",\"reason\":\"User asked to remove test memories.\",\"confidence\":0.95}\n```"})
			return nil
		}), nil
	})

	out, err := a.HandleInputAs(context.Background(), "desktop:agent:andy", "andy", "删掉误加 memory")
	if err != nil {
		t.Fatal(err)
	}
	if out != "试一下。" {
		t.Fatalf("out = %q, want fence stripped", out)
	}
	if entries, err := os.ReadDir(filepath.Join(a.Config().MemoryDir(), "persona", "andy")); err == nil && len(entries) != 0 {
		t.Fatalf("delete proposal wrote memory files = %d", len(entries))
	}
	spaces, err := a.Spaces().ListSpaces()
	if err != nil {
		t.Fatal(err)
	}
	var foundFailedCard bool
	for _, sp := range spaces {
		for _, m := range sp.Messages {
			for _, att := range m.Attachments {
				if att.Kind == "memory_commit" && strings.Contains(att.Data, `"status":"failed"`) && strings.Contains(att.Data, "memory delete proposals are not supported") {
					foundFailedCard = true
				}
			}
		}
	}
	if !foundFailedCard {
		t.Fatal("failed memory delete card missing from Space")
	}
}

func TestExternalPersonaSumiExposedWayWithoutProposalDoesNotPrecommit(t *testing.T) {
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
			turn.Session.Add(msg.Message{Role: "assistant", Content: "我会整理。"})
			return nil
		}), nil
	})

	out, err := a.HandleInputAs(context.Background(), "desktop:channel:alpha", "bob", "那你记忆一下吧，用 sumi 暴露给你的方式：我喜欢中文简洁回答")
	if err != nil {
		t.Fatal(err)
	}
	if out != "我会整理。" {
		t.Fatalf("out = %q", out)
	}
	if notice != "" {
		t.Fatalf("memory notice = %q, want no precommit without agent proposal", notice)
	}
	entries, err := os.ReadDir(filepath.Join(a.Config().MemoryDir(), "persona", "bob"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("persona memory files = %d, want 0", len(entries))
	}
	if strings.Contains(brief, "中文简洁") {
		t.Fatalf("memory brief got precommitted user text: %q", brief)
	}
}
