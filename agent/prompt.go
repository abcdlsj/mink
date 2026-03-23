package agent

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abcdlsj/mink/config"
)

type section struct {
	head string
	body func() string
}

func (s section) render() string {
	text := s.body()
	if text == "" {
		return ""
	}
	if s.head != "" {
		return "## " + s.head + "\n" + text + "\n\n"
	}
	return text + "\n\n"
}

func (a *Agent) buildPrompt(src string) string {
	sections := []section{
		a.sectionBase(),
		a.sectionContext(),
		a.sectionSoul(),
		a.sectionTelegram(src),
		a.sectionCustom(),
		a.sectionTools(),
		a.sectionSpawn(),
	}

	var b strings.Builder
	for _, s := range sections {
		b.WriteString(s.render())
	}
	return b.String()
}

func (a *Agent) sectionBase() section {
	return section{body: func() string {
		return strings.Join([]string{
			"You are an autonomous agent with full access to tools.",
			"Act, don't ask. Use tools to investigate, execute, and verify.",
			"When a task is unclear, check context and tools first, ask only when truly stuck.",
			"Be direct and concise. Give conclusions and actionable results first.",
			"When tools are needed, call tools first and give the user-facing answer only after the tool work is complete.",
			"Never repeat the same tool call with unchanged arguments in the same turn; reuse prior results or change strategy.",
		}, "\n")
	}}
}

func (a *Agent) sectionContext() section {
	return section{body: func() string {
		var b strings.Builder
		if pwd, _ := os.Getwd(); pwd != "" {
			fmt.Fprintf(&b, "Working directory: %s\n", pwd)
		}
		fmt.Fprintf(&b, "Current time: %s", time.Now().Format("2006-01-02 15:04:05"))
		return b.String()
	}}
}

func (a *Agent) sectionSoul() section {
	return section{head: "Persona", body: loadSoulPrompt}
}

func (a *Agent) sectionTelegram(src string) section {
	return section{head: "Telegram", body: func() string {
		if !strings.EqualFold(a.cfg.Mode, "tg") || !strings.HasPrefix(src, "telegram:") || a.subAgent {
			return ""
		}
		mention := strings.ToLower(strings.TrimSpace(a.cfg.TelegramMentionMode))
		if mention == "" {
			mention = "always"
		}
		scope := strings.ToLower(strings.TrimSpace(a.cfg.TelegramSessionScope))
		if scope == "" {
			scope = "chat"
		}
		scopeLine := "- Session scope: chat-wide context."
		if scope == "thread" {
			scopeLine = "- Session scope: per-thread context when thread_id exists."
		}
		mentionLine := "- Group delivery mode: all group messages may be forwarded."
		switch mention {
		case "mention_only":
			mentionLine = "- Group delivery mode: you'll mainly receive @mentioned/reply messages."
		case "smart":
			mentionLine = "- Group delivery mode: selective prefiltering is enabled."
		}
		return strings.Join([]string{
			"You operate inside Telegram chats. Messages include [telegram_context]...[/telegram_context] with sender, mention status, message id, thread id, and reply chain.",
			"",
			"Behavior:",
			mentionLine,
			scopeLine,
			"- If no reply is needed, respond with exactly: NO_REPLY",
			"- Default to short, chat-friendly replies. Save long output for when asked.",
			"",
			"Directives (stripped before sending, never explain to users):",
			"- [[reply_to_current]] or [[reply_to:<message_id>]] — control reply target",
			"- [[react:👍]] — add emoji reaction (often better than text: expressive + saves tokens)",
		}, "\n")
	}}
}

func (a *Agent) sectionCustom() section {
	return section{body: func() string { return a.prompt }}
}

func (a *Agent) sectionTools() section {
	return section{head: "Tools", body: func() string {
		var b strings.Builder
		for _, t := range a.reg.All() {
			fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Desc())
		}
		return b.String()
	}}
}

func (a *Agent) sectionSpawn() section {
	return section{head: fmt.Sprintf("Multi-Agent (max %d concurrent)", maxActiveSubAgents), body: func() string {
		if a.reg.Get("spawn") == nil {
			return ""
		}
		return strings.Join([]string{
			"`spawn` runs a child-agent subtask and returns its final result. `direct_output: true` also streams the child output directly to the user.",
			"`background` runs long commands asynchronously. You'll be notified when done.",
		}, "\n")
	}}
}

func loadSoulPrompt() string {
	data, err := os.ReadFile(config.SoulPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
