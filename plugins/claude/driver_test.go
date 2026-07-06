package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/plugins/external"
)

func TestDriverFormatsSessionHistory(t *testing.T) {
	d := driver()
	if d.FormatHistory == nil {
		t.Fatal("FormatHistory is nil")
	}

	out := d.FormatHistory([]msg.Message{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好，有什么可以帮你的"},
		{Role: "assistant", ToolCalls: []msg.ToolCall{{
			Name: "bash",
			Args: []byte(`{"cmd":"pwd"}`),
		}}},
		{Role: "tool", ToolResults: []msg.ToolResult{{
			Content: "/tmp/demo",
		}}},
	})

	for _, want := range []string{
		"<conversation_history>",
		`[user]: "你好"`,
		`[assistant]: "你好，有什么可以帮你的"`,
		`[tool_call]: bash({"cmd":"pwd"})`,
		`[tool_result]: "/tmp/demo"`,
		"</conversation_history>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("history missing %q:\n%s", want, out)
		}
	}
}

func TestDriverBuildArgsDoesNotRequestPartialMessages(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "", false)
	for _, arg := range args {
		if arg == "--include-partial-messages" {
			t.Fatalf("unexpected arg %q", arg)
		}
	}
}

func TestDriverBuildArgsBypassesInteractivePermissions(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "", false)
	foundDanger := false
	foundMode := false
	for i, arg := range args {
		if arg == "--permission-mode" && i+1 < len(args) && args[i+1] == "bypassPermissions" {
			foundMode = true
		}
		if arg == "--dangerously-skip-permissions" {
			foundDanger = true
		}
	}
	if !foundMode || !foundDanger {
		t.Fatalf("permission bypass args not found: %v", args)
	}
}

func TestDriverBuildArgsIgnoresExternalSessionID(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "test-session-123", false)
	for _, forbidden := range []string{"--session-id", "test-session-123", "--resume"} {
		if contains(args, forbidden) {
			t.Fatalf("stateless claude args contain %q: %v", forbidden, args)
		}
	}
}

func TestDriverBuildArgsDoesNotResumeExternalSession(t *testing.T) {
	args := driver().BuildArgs("hello", "/tmp/demo", "test-session-123", true)
	for _, forbidden := range []string{"--resume", "test-session-123", "--session-id"} {
		if contains(args, forbidden) {
			t.Fatalf("stateless claude args contain %q: %v", forbidden, args)
		}
	}
}

func TestDriverBuildArgsUsesIsolatedProfile(t *testing.T) {
	profile := external.Profile{
		Isolated:     true,
		SettingsPath: "/tmp/sumi/external/claude/bob/claude/settings.json",
		PluginDirs:   []string{"/tmp/sumi/external/claude/bob/claude/plugins"},
	}
	args := driver().BuildArgsWithProfile("hello", "/tmp/demo", "test-session-123", true, profile)
	for _, want := range []string{
		"--bare",
		"--no-session-persistence",
		"--settings",
		profile.SettingsPath,
		"--plugin-dir",
		profile.PluginDirs[0],
		"--add-dir",
		"/tmp/demo",
	} {
		if !contains(args, want) {
			t.Fatalf("isolated args missing %q: %v", want, args)
		}
	}
	for _, forbidden := range []string{"--resume", "test-session-123", "--session-id"} {
		if contains(args, forbidden) {
			t.Fatalf("isolated args contain %q: %v", forbidden, args)
		}
	}
}

func TestParseOutputMarksAssistantSnapshot(t *testing.T) {
	m := parseOutput(mustJSON(t, map[string]any{
		"type":    "assistant",
		"subtype": "message",
		"message": map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "hello",
			}},
		},
	}))
	if m == nil {
		t.Fatalf("parseOutput = nil")
	}
	if m.Type != external.MsgAssistantText || !m.Snapshot || m.Text != "hello" {
		t.Fatalf("assistant got %#v", m)
	}
}

func contains(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func TestParseOutputResultEmitsTurnDoneSnapshot(t *testing.T) {
	m := parseOutput(mustJSON(t, map[string]any{
		"type":   "result",
		"result": "hello",
	}))
	if m == nil {
		t.Fatalf("parseOutput = nil")
	}
	if m.Type != external.MsgTurnDone || m.Text != "hello" {
		t.Fatalf("result got %#v", m)
	}
}

func TestParseOutputCapturesThinking(t *testing.T) {
	m := parseOutput(mustJSON(t, map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"type": "content_block_delta",
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": "think",
			},
		},
	}))
	if m == nil || m.Type != external.MsgThinkingChunk || m.Text != "think" {
		t.Fatalf("got %#v", m)
	}
}

func TestParseOutputCapturesResultErrorDetails(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "result",
			line: mustJSON(t, map[string]any{
				"type":     "result",
				"is_error": true,
				"result":   "budget exceeded",
			}),
			want: "budget exceeded",
		},
		{
			name: "message",
			line: mustJSON(t, map[string]any{
				"type":     "result",
				"is_error": true,
				"subtype":  "error_during_execution",
				"error": map[string]any{
					"type":    "api_error",
					"message": "invalid auth",
				},
			}),
			want: "invalid auth",
		},
		{
			name: "subtype",
			line: mustJSON(t, map[string]any{
				"type":     "result",
				"is_error": true,
				"subtype":  "error_during_execution",
			}),
			want: "error_during_execution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parseOutput(tt.line)
			if m == nil || m.Type != external.MsgError || m.Text != tt.want {
				t.Fatalf("got %#v, want %q", m, tt.want)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseOutputSystemInitEmitsRuntimeMeta(t *testing.T) {
	line := mustJSON(t, map[string]any{
		"type":                "system",
		"subtype":             "init",
		"model":               "claude-opus-4-7",
		"claude_code_version": "2.1.117",
		"permissionMode":      "bypassPermissions",
		"tools":               []string{"Bash", "Read", "Write"},
		"mcp_servers": []map[string]any{
			{"name": "context7", "status": "connected"},
			{"name": "superset", "status": "connected"},
		},
	})
	m := parseOutput(line)
	if m == nil || m.Type != external.MsgRuntimeMeta {
		t.Fatalf("got %#v", m)
	}
	if m.Meta["runtime"] != "claude" || m.Meta["model"] != "claude-opus-4-7" || m.Meta["version"] != "2.1.117" {
		t.Fatalf("meta = %#v", m.Meta)
	}
	if m.Meta["tools_count"] != "3" || m.Meta["mcp_servers_count"] != "2" {
		t.Fatalf("counts = %#v", m.Meta)
	}
}

func TestParseOutputCapturesToolResult(t *testing.T) {
	line := mustJSON(t, map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": "tooluse_xyz",
				"content":     "63 files",
				"is_error":    false,
			}},
		},
		"tool_use_result": map[string]any{
			"stdout": "63 files",
			"stderr": "",
		},
	})
	m := parseOutput(line)
	if m == nil {
		t.Fatal("parseOutput = nil")
	}
	if m.Type != external.MsgToolResult || m.ToolID != "tooluse_xyz" || m.Text != "63 files" || m.IsError {
		t.Fatalf("got %#v", m)
	}
}

func TestParseOutputResultEmitsTurnDoneWithUsage(t *testing.T) {
	line := mustJSON(t, map[string]any{
		"type":           "result",
		"result":         "done",
		"total_cost_usd": 0.42,
		"usage": map[string]any{
			"input_tokens":                100,
			"output_tokens":               20,
			"cache_creation_input_tokens": 5,
			"cache_read_input_tokens":     0,
		},
		"modelUsage": map[string]any{
			"claude-opus-4-7": map[string]any{
				"costUSD":         0.42,
				"contextWindow":   200000,
				"maxOutputTokens": 32000,
			},
		},
		"terminal_reason": "completed",
	})
	m := parseOutput(line)
	if m == nil {
		t.Fatal("parseOutput = nil")
	}
	if m.Type != external.MsgTurnDone {
		t.Fatalf("type = %v, want MsgTurnDone", m.Type)
	}
	if m.CostUSD != 0.42 || m.Reason != "completed" || m.Model != "" {
		t.Fatalf("meta = %#v", m)
	}
	if m.Usage == nil || m.Usage.Input != 105 || m.Usage.Output != 20 || m.Usage.ContextWindow != 200000 {
		t.Fatalf("usage = %#v", m.Usage)
	}
}

func TestParseOutputResultUsesTopLevelModelOnly(t *testing.T) {
	line := mustJSON(t, map[string]any{
		"type":   "result",
		"result": "done",
		"model":  "claude-sonnet-4-6",
		"modelUsage": map[string]any{
			"claude-haiku-4-5-20251001": map[string]any{"costUSD": 0.01},
		},
	})
	m := parseOutput(line)
	if m == nil || m.Type != external.MsgTurnDone {
		t.Fatalf("got %#v", m)
	}
	if m.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want top-level model", m.Model)
	}
}
