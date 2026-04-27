package agent

import (
	"os"
	"strings"

	"github.com/abcdlsj/mink/session"
)

func BuildSystemPrompt(env *RuntimeEnv, t *Turn) string {
	b := promptBuilder{env: env, turn: t}
	return b.system()
}

func BuildExternalPrompt(env *RuntimeEnv, t *Turn, hist string) string {
	var p promptEnvelope
	if sys := strings.TrimSpace(BuildSystemPrompt(env, t)); sys != "" {
		p.Add("system_prompt", sys)
	}
	if hist = strings.TrimSpace(hist); hist != "" {
		p.AddRaw(hist)
	}
	if t != nil && strings.TrimSpace(t.Input) != "" {
		p.Add("user_message", t.Input)
	}
	return p.String()
}

type promptBuilder struct {
	env  *RuntimeEnv
	turn *Turn
}

func (b promptBuilder) system() string {
	var p promptSections
	p.Add(b.base())
	p.Add(b.context())
	p.Add(b.soul())
	p.Add(b.telegram())
	p.Add(b.custom())
	return p.String()
}

func (b promptBuilder) base() string {
	return strings.Join([]string{
		"You are Mink, a local coding agent.",
		"Work directly and concisely.",
		"Use tools when needed. Prefer read before edit.",
		"Keep changes within the workspace unless the user asks otherwise.",
	}, "\n")
}

func (b promptBuilder) context() string {
	var lines []string
	if b.env != nil && strings.TrimSpace(b.env.Workspace) != "" {
		lines = append(lines, "Workspace: "+strings.TrimSpace(b.env.Workspace))
	}
	if s := b.session(); s != nil && strings.TrimSpace(s.Summary) != "" {
		lines = append(lines, "Conversation summary:\n"+strings.TrimSpace(s.Summary))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (b promptBuilder) soul() string {
	if b.env == nil {
		return ""
	}
	return loadSoulPrompt(b.env.SoulPath)
}

func (b promptBuilder) telegram() string {
	if !strings.HasPrefix(b.source(), "telegram:") {
		return ""
	}
	mention := normalizeTelegramMention(b.env)
	scope := normalizeTelegramScope(b.env)
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
		"You operate inside Telegram chats. Input is forwarded Telegram chat text after mention filtering.",
		"",
		"Behavior:",
		mentionLine,
		scopeLine,
		"- If no reply is needed, respond with exactly: NO_REPLY",
		"- Default to short, chat-friendly replies. Save long output for when asked.",
		"",
		"Directives:",
		"- [[reply_to_current]] or [[reply_to:<message_id>]]",
		"- [[react:👍]]",
	}, "\n")
}

func (b promptBuilder) custom() string {
	if b.env == nil {
		return ""
	}
	return strings.TrimSpace(b.env.Prompt)
}

func (b promptBuilder) session() *session.Session {
	if b.turn == nil {
		return nil
	}
	return b.turn.Session
}

func (b promptBuilder) source() string {
	if b.turn == nil {
		return ""
	}
	return strings.TrimSpace(b.turn.Source)
}

func normalizeTelegramMention(env *RuntimeEnv) string {
	if env == nil {
		return "always"
	}
	switch strings.TrimSpace(strings.ToLower(env.TelegramMentionMode)) {
	case "", "always":
		return "always"
	case "smart":
		return "smart"
	case "mention_only":
		return "mention_only"
	default:
		return "always"
	}
}

func normalizeTelegramScope(env *RuntimeEnv) string {
	if env == nil {
		return "chat"
	}
	switch strings.TrimSpace(strings.ToLower(env.TelegramSessionScope)) {
	case "", "chat":
		return "chat"
	case "thread":
		return "thread"
	default:
		return "chat"
	}
}

func loadSoulPrompt(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
