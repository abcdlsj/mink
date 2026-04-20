package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/abcdlsj/mink/session"
)

func TestBuildSystemPromptAddsSoulAndTelegramSections(t *testing.T) {
	dir := t.TempDir()
	soul := dir + "/SOUL.md"
	if err := os.WriteFile(soul, []byte("保持锋利"), 0644); err != nil {
		t.Fatal(err)
	}

	env := &RuntimeEnv{
		Workspace:            "/tmp/work",
		Prompt:               "项目约束",
		SoulPath:             soul,
		TelegramMentionMode:  "smart",
		TelegramSessionScope: "thread",
	}
	turn := &Turn{
		Source:  "telegram:1:9",
		Session: &session.Session{Summary: "历史摘要"},
	}

	out := BuildSystemPrompt(env, turn)
	for _, want := range []string{
		"You are Mink, a local coding agent.",
		"Workspace: /tmp/work",
		"Conversation summary:\n历史摘要",
		"保持锋利",
		"项目约束",
		"[telegram_context]",
		"Group delivery mode: selective prefiltering is enabled.",
		"Session scope: per-thread context when thread_id exists.",
		"NO_REPLY",
		"[[reply_to_current]]",
		"[[react:👍]]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}
}

func TestBuildSystemPromptSkipsTelegramOutsideTelegramSource(t *testing.T) {
	env := &RuntimeEnv{
		TelegramMentionMode:  "mention_only",
		TelegramSessionScope: "thread",
	}
	turn := &Turn{
		Source:  "cli",
		Session: session.New("cli"),
	}

	out := BuildSystemPrompt(env, turn)
	if strings.Contains(out, "[telegram_context]") {
		t.Fatalf("unexpected telegram section:\n%s", out)
	}
}

func TestBuildExternalPromptWrapsSystemHistoryAndInput(t *testing.T) {
	env := &RuntimeEnv{Prompt: "项目约束"}
	turn := &Turn{
		Source:  "cli",
		Input:   "修这个 bug",
		Session: &session.Session{Summary: "旧上下文"},
	}

	out := BuildExternalPrompt(env, turn, "<conversation_history>\n[user]: hi\n</conversation_history>")
	for _, want := range []string{
		"<system_prompt>",
		"项目约束",
		"</system_prompt>",
		"<conversation_history>",
		"<user_message>",
		"修这个 bug",
		"</user_message>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("external prompt missing %q:\n%s", want, out)
		}
	}
}
