package external

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

func TestHandleMessageDoesNotRepublishFinalAssistantTextAfterStreaming(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	handleMessage("test", turn, st, &Message{Type: MsgStreamChunk, Text: "hello"})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hello", Snapshot: true})
	st.flush(turn.Session)

	var chunks []string
	for {
		select {
		case ev := <-evs:
			if ev.Type == bus.TurnChunk {
				chunks = append(chunks, ev.Text)
			}
		default:
			if got := strings.Join(chunks, ""); got != "hello" {
				t.Fatalf("chunks = %q, want %q", got, "hello")
			}
			if got := turn.Session.Messages[len(turn.Session.Messages)-1].Content; got != "hello" {
				t.Fatalf("assistant = %q, want %q", got, "hello")
			}
			return
		}
	}
}

func TestHandleMessageMergesAssistantSnapshots(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hel", Snapshot: true})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hello", Snapshot: true})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "hello", Snapshot: true})
	st.flush(turn.Session)

	var chunks []string
	for {
		select {
		case ev := <-evs:
			if ev.Type == bus.TurnChunk {
				chunks = append(chunks, ev.Text)
			}
		default:
			if got := strings.Join(chunks, ""); got != "hello" {
				t.Fatalf("chunks = %q, want %q", got, "hello")
			}
			if got := turn.Session.Messages[len(turn.Session.Messages)-1].Content; got != "hello" {
				t.Fatalf("assistant = %q, want %q", got, "hello")
			}
			return
		}
	}
}

func TestHandleMessageDedupsTrailingSnapshotAfterAppend(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	// First snapshot: prelude.
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "intro 喵", Snapshot: true})
	// Second snapshot: a fresh report (driver appends because neither prefix matches).
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "## report\nbody", Snapshot: true})
	// Third arrival is the same report (eg a result.result mirror); must not append again.
	handleMessage("test", turn, st, &Message{Type: MsgTurnDone, Text: "## report\nbody"})
	st.flush(turn.Session)

	var chunks []string
	for {
		select {
		case ev := <-evs:
			if ev.Type == bus.TurnChunk {
				chunks = append(chunks, ev.Text)
			}
		default:
			joined := strings.Join(chunks, "")
			want := "intro 喵\n\n## report\nbody"
			if joined != want {
				t.Fatalf("chunks = %q, want %q", joined, want)
			}
			final := turn.Session.Messages[len(turn.Session.Messages)-1].Content
			if final != want {
				t.Fatalf("assistant = %q, want %q", final, want)
			}
			return
		}
	}
}

func TestHandleMessageDedupsOverlappingFinalResult(t *testing.T) {
	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     bus.New(),
	}
	st := &runState{}
	report := "## 日志查询结果\n\n### 查询条件\n- 服务：`main.app-svr.app-opus`\n\n### 错误分组\n\n| 类型 | 数量 | 关键信息 |\n|---|---|---|\n| `dynBrief not found` | 5 | 同一 traceId |\n\n### 结论\n\n10 分钟窗口看下来没有异常。"

	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "找到了。## 日志查询结果\n\n### 查询条件\n- 服务：`main.app-svr.app-opus`\n\n### 错误分组\n\n| 类型 | 数量 | 关键信息 |\n|---|---|---|\n| `dynBrief not found` | 5 | 同一 traceId |\n\n### 结论\n\n10 分钟窗口看下来没有异常。\n\n工作目录说明。", Snapshot: true})
	handleMessage("test", turn, st, &Message{Type: MsgTurnDone, Text: report + "\n\n工作目录说明。"})
	st.flush(turn.Session)

	final := turn.Session.Messages[len(turn.Session.Messages)-1].Content
	if strings.Count(final, "## 日志查询结果") != 1 {
		t.Fatalf("final duplicated report heading:\n%s", final)
	}
	if !strings.Contains(final, "找到了。\n\n## 日志查询结果") {
		t.Fatalf("final did not repair heading boundary:\n%s", final)
	}
}

func TestFlushKeepsMissingToolResultsEmpty(t *testing.T) {
	turn := &agent.Turn{Source: "test", Session: session.New("test")}
	st := &runState{calls: map[string]toolCallState{}}

	handleMessage("test", turn, st, &Message{Type: MsgToolCall, ToolID: "a", ToolName: "bash", ToolArgs: "{}"})
	handleMessage("test", turn, st, &Message{Type: MsgToolCall, ToolID: "b", ToolName: "read", ToolArgs: "{}"})
	handleMessage("test", turn, st, &Message{Type: MsgToolResult, ToolID: "a", Text: "ok"})
	st.flush(turn.Session)

	var assistant, tool msg.Message
	for _, m := range turn.Session.Messages {
		if m.Role == "assistant" {
			assistant = m
		}
		if m.Role == "tool" {
			tool = m
		}
	}
	if len(assistant.ToolCalls) != 2 {
		t.Fatalf("assistant tool_calls = %d, want 2", len(assistant.ToolCalls))
	}
	if len(tool.ToolResults) != 2 {
		t.Fatalf("tool results = %d, want 2: %+v", len(tool.ToolResults), tool.ToolResults)
	}
	ids := map[string]string{}
	for _, tr := range tool.ToolResults {
		ids[tr.ToolCallID] = tr.Content
	}
	if ids["a"] != "ok" {
		t.Fatalf("a content = %q", ids["a"])
	}
	if ids["b"] != "" {
		t.Fatalf("b content = %q, want empty (no synthetic filler)", ids["b"])
	}
}

func TestRuntimeMetaPersistsOnAssistantMessage(t *testing.T) {
	turn := &agent.Turn{Source: "test", Session: session.New("test"), Bus: bus.New(), AgentID: "coder"}
	st := &runState{calls: map[string]toolCallState{}}

	handleMessage("test", turn, st, &Message{Type: MsgRuntimeMeta, Meta: map[string]string{
		"runtime":     "codex",
		"cli_version": "codex 1.2.3",
	}})
	handleMessage("test", turn, st, &Message{Type: MsgRuntimeMeta, Meta: map[string]string{
		"thread_id": "abc",
	}})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "ok"})
	st.flush(turn.Session)

	if len(turn.Session.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(turn.Session.Messages))
	}
	meta := turn.Session.Messages[0].RuntimeMeta
	if meta["runtime"] != "codex" || meta["cli_version"] != "codex 1.2.3" || meta["thread_id"] != "abc" {
		t.Fatalf("runtime meta = %#v", meta)
	}
}

func TestRuntimeMetaModelFillsUsageModel(t *testing.T) {
	turn := &agent.Turn{Source: "test", Session: session.New("test"), Bus: bus.New(), AgentID: "coder"}
	st := &runState{calls: map[string]toolCallState{}}

	handleMessage("test", turn, st, &Message{Type: MsgRuntimeMeta, Meta: map[string]string{
		"runtime": "claude",
		"model":   "claude-sonnet-4-6",
	}})
	handleMessage("test", turn, st, &Message{Type: MsgAssistantText, Text: "ok"})
	handleMessage("test", turn, st, &Message{Type: MsgTurnDone, Usage: &msg.TokenUsage{Input: 1, Output: 2}})
	st.flush(turn.Session)

	if len(turn.Session.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(turn.Session.Messages))
	}
	usage := turn.Session.Messages[0].Usage
	if usage == nil || usage.Model != "claude-sonnet-4-6" {
		t.Fatalf("usage = %#v, want runtime meta model", usage)
	}
}

func TestDriverRuntimeMetaPublishesBeforeCommand(t *testing.T) {
	r := &Runtime{
		driver: Driver{
			Name:    "test",
			Command: "missing-sumi-runtime-command",
			BuildArgs: func(prompt, workDir, sessionID string, resume bool) []string {
				return nil
			},
			ParseOutput: func(line string) *Message { return nil },
			RuntimeMeta: func(context.Context) map[string]string {
				return map[string]string{"runtime": "test", "cli_version": "test 1.0"}
			},
		},
		env: &agent.RuntimeEnv{},
	}
	b := bus.New()
	evs, cancel := b.Subscribe(4)
	defer cancel()
	turn := &agent.Turn{Source: "test", Session: session.New("test"), Bus: b}

	_ = r.runCommand(context.Background(), turn, newRunState(), "hi", "", false, Profile{Env: r.env.ChildEnv})

	select {
	case ev := <-evs:
		if ev.Type != bus.RuntimeInfo || !strings.Contains(ev.Text, "test 1.0") {
			t.Fatalf("event = %#v", ev)
		}
	default:
		t.Fatal("missing runtime.info event")
	}
}

func TestRunCommandUsesRuntimeChildEnv(t *testing.T) {
	r := &Runtime{
		driver: Driver{
			Name:    "test",
			Command: "/bin/sh",
			BuildArgs: func(prompt, workDir, sessionID string, resume bool) []string {
				return []string{"-c", "printf '%s\n' \"$SUMI_CHILD_ENV_TEST\""}
			},
			ParseOutput: func(line string) *Message {
				return &Message{Type: MsgAssistantText, Text: strings.TrimSpace(line)}
			},
		},
		env: &agent.RuntimeEnv{
			ChildEnv: []string{"PATH=" + os.Getenv("PATH"), "SUMI_CHILD_ENV_TEST=ok"},
		},
	}
	turn := &agent.Turn{Source: "test", Session: session.New("test"), Bus: bus.New()}
	st := newRunState()

	if err := r.runCommand(context.Background(), turn, st, "hi", "", false, Profile{Env: r.env.ChildEnv}); err != nil {
		t.Fatal(err)
	}
	st.flush(turn.Session)
	if got := turn.Session.Messages[len(turn.Session.Messages)-1].Content; got != "ok" {
		t.Fatalf("assistant = %q, want ok", got)
	}
}

func TestPrepareProfileIsolatesCodexHomeAndEnv(t *testing.T) {
	dir := t.TempDir()
	r := &Runtime{
		driver: Driver{Name: "codex"},
		env: &agent.RuntimeEnv{
			DataRoot: dir,
			ChildEnv: []string{
				"PATH=/bin",
				"HOME=/host/home",
				"CODEX_HOME=/host/codex",
				"CLAUDE_CONFIG_DIR=/host/claude",
				"OPENAI_API_KEY=ok",
				"SUMI_EMBY_SERVER=https://emby.example",
			},
			Persona: &agent.Persona{ID: "Bob Agent"},
		},
	}

	profile, err := r.prepareProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Isolated {
		t.Fatal("profile is not isolated")
	}
	if !strings.HasPrefix(profile.Root, filepath.Join(dir, "external", "codex")) {
		t.Fatalf("profile root = %q", profile.Root)
	}
	env := envMap(profile.Env)
	if got := env["HOME"]; got == "/host/home" || !strings.HasPrefix(got, profile.Root) {
		t.Fatalf("HOME = %q", got)
	}
	if got := env["CODEX_HOME"]; got == "/host/codex" || got != profile.CodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q", got, profile.CodexHome)
	}
	if _, ok := env["CLAUDE_CONFIG_DIR"]; ok {
		t.Fatal("CLAUDE_CONFIG_DIR leaked into isolated profile env")
	}
	if got := env["SUMI_EMBY_SERVER"]; got != "https://emby.example" {
		t.Fatalf("SUMI_EMBY_SERVER = %q", got)
	}
}

func TestPrepareProfileFailsClosedWithoutClaudeAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := &Runtime{
		driver: Driver{Name: "claude"},
		env: &agent.RuntimeEnv{
			DataRoot: t.TempDir(),
			ChildEnv: []string{
				"PATH=/bin",
			},
		},
	}

	_, err := r.prepareProfile()
	if err == nil || !strings.Contains(err.Error(), "refusing to fall back to host ~/.claude") {
		t.Fatalf("err = %v, want fail closed", err)
	}
}

func TestPrepareProfileImportsHostClaudeProfile(t *testing.T) {
	hostHome := t.TempDir()
	hostClaude := filepath.Join(hostHome, ".claude")
	if err := os.MkdirAll(filepath.Join(hostClaude, "plugins", "local-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hostClaude, "commands"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hostClaude, "skills", "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostClaude, "config.json"), []byte(`{"primaryApiKey":"host-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostClaude, "settings.json"), []byte(`{"apiKeyHelper":"helper"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostClaude, "plugins", "local-plugin", "plugin.json"), []byte(`{"name":"local-plugin"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostClaude, "commands", "ship.md"), []byte("# Ship\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostClaude, "skills", "demo", "SKILL.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(hostClaude, "config.json"), filepath.Join(hostClaude, "skills", "linked-config.json")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv("HOME", hostHome)

	r := &Runtime{
		driver: Driver{Name: "claude"},
		env: &agent.RuntimeEnv{
			DataRoot: t.TempDir(),
			ChildEnv: []string{
				"PATH=/bin",
			},
			Persona: &agent.Persona{ID: "Bob"},
		},
	}

	profile, err := r.prepareProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Isolated {
		t.Fatal("profile is not isolated")
	}
	env := envMap(profile.Env)
	if got := env["HOME"]; got == hostHome || !strings.HasPrefix(got, profile.Root) {
		t.Fatalf("HOME = %q", got)
	}
	if _, err := os.Stat(filepath.Join(profile.Home, ".claude", "config.json")); err != nil {
		t.Fatalf("missing imported claude config: %v", err)
	}
	if _, err := os.Stat(profile.SettingsPath); err != nil {
		t.Fatalf("missing imported claude settings: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profile.PluginDirs[0], "local-plugin", "plugin.json")); err != nil {
		t.Fatalf("missing imported claude plugin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profile.Home, ".claude", "commands", "ship.md")); err != nil {
		t.Fatalf("missing imported claude command: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profile.Home, ".claude", "skills", "demo", "SKILL.md")); err != nil {
		t.Fatalf("missing imported claude skill: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(profile.Home, ".claude", "skills", "linked-config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("imported symlink = %v, want skipped", err)
	}
}

func TestPrepareProfileFailsClosedWithoutCodexAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := &Runtime{
		driver: Driver{Name: "codex"},
		env: &agent.RuntimeEnv{
			DataRoot: t.TempDir(),
			ChildEnv: []string{
				"PATH=/bin",
			},
		},
	}

	_, err := r.prepareProfile()
	if err == nil || !strings.Contains(err.Error(), "refusing to fall back to host ~/.codex") {
		t.Fatalf("err = %v, want fail closed", err)
	}
}

func TestPrepareProfileImportsHostCodexProviderConfig(t *testing.T) {
	hostHome := t.TempDir()
	hostCodex := filepath.Join(hostHome, ".codex")
	if err := os.MkdirAll(hostCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	hostConfig := `
model_provider = "aicoding"
model = "gpt-5.5"
disable_response_storage = true

[model_providers.aicoding]
name = "aicoding"
base_url = "http://api-ai-coding.example/api/v1/codex"
wire_api = "responses"
env_key = "AICODING_API_KEY"

[mcp_servers.secret]
url = "https://mcp.example"
api_token = "do-not-copy"

[projects."/tmp/project"]
trust_level = "trusted"
`
	if err := os.WriteFile(filepath.Join(hostCodex, "config.toml"), []byte(hostConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", hostHome)
	t.Setenv("AICODING_API_KEY", "host-provider-key")

	r := &Runtime{
		driver: Driver{Name: "codex"},
		env: &agent.RuntimeEnv{
			DataRoot: t.TempDir(),
			ChildEnv: []string{
				"PATH=/bin",
			},
			Persona: &agent.Persona{ID: "Bob"},
		},
	}
	staleProfileConfig := filepath.Join(r.env.DataRoot, "external", "codex", "bob", "codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(staleProfileConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleProfileConfig, []byte(`[projects."/tmp/project"]`+"\n"+`trust_level = "trusted"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	profile, err := r.prepareProfile()
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(profile.CodexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, want := range []string{
		`model_provider = "aicoding"`,
		`[model_providers.aicoding]`,
		`env_key = "AICODING_API_KEY"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("codex config missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"mcp_servers", "do-not-copy", "projects."} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("codex config copied forbidden %q:\n%s", forbidden, text)
		}
	}
	env := envMap(profile.Env)
	if got := env["CODEX_HOME"]; got != profile.CodexHome {
		t.Fatalf("CODEX_HOME = %q, want %q", got, profile.CodexHome)
	}
	if got := env["AICODING_API_KEY"]; got != "host-provider-key" {
		t.Fatalf("AICODING_API_KEY = %q", got)
	}
}

func TestHandleMessagePublishesThinking(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	handleMessage("test", turn, st, &Message{Type: MsgThinkingChunk, Text: "think"})
	st.flush(turn.Session)

	select {
	case ev := <-evs:
		if ev.Type != bus.TurnReasoning || ev.Text != "think" {
			t.Fatalf("event = %#v", ev)
		}
	default:
		t.Fatal("missing thinking event")
	}
	if got := turn.Session.Messages[len(turn.Session.Messages)-1].Reasoning; got != "think" {
		t.Fatalf("reasoning = %q", got)
	}
}

func TestRuntimeBuildPromptUsesSharedSystemPrompt(t *testing.T) {
	r := &Runtime{
		driver: Driver{
			FormatHistory: func(messages []msg.Message) string {
				return "<conversation_history>\n[user]: old\n</conversation_history>"
			},
		},
		env: &agent.RuntimeEnv{
			Prompt: "项目约束",
		},
	}
	turn := &agent.Turn{
		Source:  "cli",
		Input:   "继续",
		Session: session.New("cli"),
	}
	turn.Session.Add(msg.Message{Role: "user", Content: "old"})

	out := r.buildPrompt(turn, true)
	for _, want := range []string{
		"<system_prompt>",
		"项目约束",
		"<conversation_history>",
		"[user]: old",
		"<user_message>",
		"继续",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}
}

func TestRuntimeBuildPromptCanOmitHistoryForResume(t *testing.T) {
	r := &Runtime{
		driver: Driver{
			FormatHistory: func(messages []msg.Message) string {
				return "<conversation_history>\n[user]: old\n</conversation_history>"
			},
		},
	}
	turn := &agent.Turn{
		Source:  "cli",
		Input:   "继续",
		Session: session.New("cli"),
	}
	turn.Session.Add(msg.Message{Role: "user", Content: "old"})

	out := r.buildPrompt(turn, false)
	if strings.Contains(out, "<conversation_history>") || strings.Contains(out, "[user]: old") {
		t.Fatalf("resume prompt should omit injected history:\n%s", out)
	}
	if !strings.Contains(out, "<user_message>") || !strings.Contains(out, "继续") {
		t.Fatalf("prompt missing current input:\n%s", out)
	}
}

func TestRuntimeSessionIDResumeFlag(t *testing.T) {
	r := &Runtime{driver: Driver{Name: "claude"}}
	s := session.New("test")

	id, resume := r.getOrCreateSessionID(s)
	if id == "" || resume {
		t.Fatalf("first = %q %v", id, resume)
	}
	got, resume := r.getOrCreateSessionID(s)
	if got != id || !resume {
		t.Fatalf("second = %q %v, want %q true", got, resume, id)
	}
}

func TestRuntimeExternalSessionCanDisableResume(t *testing.T) {
	r := &Runtime{driver: Driver{Name: "claude"}}
	s := session.New("test")
	s.ExternalSession["claude"] = "existing"

	id, resume := r.externalSession(&agent.Turn{
		Session:               s,
		DisableExternalResume: true,
	})
	if id != "" || resume {
		t.Fatalf("externalSession = %q %v, want empty false", id, resume)
	}
	if got := s.ExternalSession["claude"]; got != "existing" {
		t.Fatalf("stored session changed to %q", got)
	}
}

func TestRuntimeIsolatedProfileDoesNotCreateExternalSession(t *testing.T) {
	r := &Runtime{
		driver: Driver{
			Name:    "claude",
			Command: "/bin/sh",
			BuildArgsWithProfile: func(prompt, workDir, sessionID string, resume bool, profile Profile) []string {
				if sessionID != "" || resume {
					t.Fatalf("isolated run got sessionID=%q resume=%v", sessionID, resume)
				}
				if !profile.Isolated {
					t.Fatal("profile is not isolated")
				}
				return []string{"-c", "printf '{\"type\":\"result\",\"result\":\"ok\"}\\n'"}
			},
			ParseOutput: func(line string) *Message {
				return &Message{Type: MsgTurnDone, Text: "ok"}
			},
		},
		env: &agent.RuntimeEnv{
			DataRoot: t.TempDir(),
			ChildEnv: []string{
				"PATH=/bin",
				"ANTHROPIC_API_KEY=ok",
			},
		},
	}
	s := session.New("test")
	err := r.Run(context.Background(), &agent.Turn{
		Source:  "test",
		Input:   "hello",
		Session: s,
		Bus:     bus.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ExternalSession) != 0 {
		t.Fatalf("external session was persisted: %#v", s.ExternalSession)
	}
}

func TestRuntimeSessionIDIsScopedByWorkspace(t *testing.T) {
	s := session.New("test")
	sumi := &Runtime{driver: Driver{Name: "claude"}, workspace: "/tmp/sumi"}
	dyn := &Runtime{driver: Driver{Name: "claude"}, workspace: "/tmp/go-dynamic"}

	first, resume := sumi.getOrCreateSessionID(s)
	if first == "" || resume {
		t.Fatalf("first = %q %v", first, resume)
	}
	second, resume := dyn.getOrCreateSessionID(s)
	if second == "" || resume {
		t.Fatalf("second = %q %v", second, resume)
	}
	if first == second {
		t.Fatalf("workspace sessions share id %q", first)
	}
	if got := s.ExternalSession["claude:/tmp/sumi"]; got != first {
		t.Fatalf("sumi session = %q, want %q", got, first)
	}
	if got := s.ExternalSession["claude:/tmp/go-dynamic"]; got != second {
		t.Fatalf("dynamic session = %q, want %q", got, second)
	}
}

func TestRuntimeResetSessionIDReplacesStaleExternalSession(t *testing.T) {
	r := &Runtime{driver: Driver{Name: "claude"}}
	s := session.New("test")
	s.ExternalSession["claude"] = "stale"

	next := r.resetSessionID(s)
	if next == "" || next == "stale" {
		t.Fatalf("next = %q", next)
	}
	if got := s.ExternalSession["claude"]; got != next {
		t.Fatalf("external session = %q, want %q", got, next)
	}
}

func TestMissingExternalSessionDetectsClaudeResumeError(t *testing.T) {
	err := wrapMessageError("claude", &Message{Type: MsgError, Text: "error_during_execution: No conversation found with session ID: 67c492f4-8d18-4552-b6cc-698de0082a2d"})
	if !missingExternalSession(err) {
		t.Fatalf("missingExternalSession(%v) = false", err)
	}
	if missingExternalSession(wrapMessageError("claude", &Message{Type: MsgError, Text: "auth failed"})) {
		t.Fatal("auth failure detected as missing session")
	}
}

func TestHandleMessageReturnsMsgErrorWithoutPublishingTurnError(t *testing.T) {
	b := bus.New()
	evs, cancel := b.Subscribe(8)
	defer cancel()

	turn := &agent.Turn{
		Source:  "test",
		Session: session.New("test"),
		Bus:     b,
	}
	st := &runState{}

	err := handleMessage("test", turn, st, &Message{Type: MsgError, Text: "boom"})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}

	select {
	case ev := <-evs:
		t.Fatalf("unexpected event: %#v", ev)
	default:
	}
}

func TestRuntimeStartErrorIncludesRuntimeName(t *testing.T) {
	r := &Runtime{driver: Driver{
		Name:        "missing",
		Command:     "__sumi_missing_runtime_binary__",
		BuildArgs:   func(prompt, workDir, sessionID string, resume bool) []string { return nil },
		ParseOutput: func(line string) *Message { return nil },
	}}
	err := r.Run(context.Background(), &agent.Turn{
		Source:  "test",
		Input:   "hello",
		Session: session.New("test"),
		Bus:     bus.New(),
	})
	if err == nil || !strings.Contains(err.Error(), "missing unavailable:") {
		t.Fatalf("err = %v, want labeled start failure", err)
	}
}

func TestSummarizeStderrCollapsesRepeatedWebsocketErrors(t *testing.T) {
	stderr := strings.Join([]string{
		"2026-06-23T10:31:13.842645Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket: IO error: Connection reset by peer (os error 54), url: wss://api.openai.com/v1/responses",
		"2026-06-23T10:31:14.209902Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket: IO error: Connection reset by peer (os error 54), url: wss://api.openai.com/v1/responses",
	}, "\n")
	got := summarizeStderr(stderr)
	if got != "failed to connect to websocket: connection reset by peer" {
		t.Fatalf("summary = %q", got)
	}
	if strings.Contains(got, "wss://") || strings.Contains(got, "2026-") {
		t.Fatalf("summary leaked raw stderr details: %q", got)
	}
}

func TestRuntimeExitErrorIncludesRuntimeName(t *testing.T) {
	r := &Runtime{driver: Driver{
		Name:    "shell",
		Command: "/bin/sh",
		BuildArgs: func(prompt, workDir, sessionID string, resume bool) []string {
			return []string{"-c", "exit 7"}
		},
		ParseOutput: func(line string) *Message { return nil },
	}}
	err := r.Run(context.Background(), &agent.Turn{
		Source:  "test",
		Input:   "hello",
		Session: session.New("test"),
		Bus:     bus.New(),
	})
	if err == nil || !strings.Contains(err.Error(), "shell exited:") || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("err = %v, want labeled exit failure", err)
	}
}

func TestRuntimeContextDeadlineIncludesRuntimeName(t *testing.T) {
	r := &Runtime{driver: Driver{
		Name:    "shell",
		Command: "/bin/sh",
		BuildArgs: func(prompt, workDir, sessionID string, resume bool) []string {
			return []string{"-c", "sleep 2"}
		},
		ParseOutput: func(line string) *Message { return nil },
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := r.Run(ctx, &agent.Turn{
		Source:  "test",
		Input:   "hello",
		Session: session.New("test"),
		Bus:     bus.New(),
	})
	if err == nil || !strings.Contains(err.Error(), "shell timed out:") {
		t.Fatalf("err = %v, want labeled timeout failure", err)
	}
}
