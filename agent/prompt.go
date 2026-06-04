package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/abcdlsj/sumi/session"
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
	p.Add(b.persona())
	p.Add(b.context())
	p.Add(b.preferences())
	p.Add(b.permissions())
	p.Add(b.soul())
	p.Add(b.telegram())
	p.Add(b.custom())
	return p.String()
}

func (b promptBuilder) base() string {
	return strings.Join([]string{
		"You are Sumi, a local coding agent.",
		"Work directly and concisely.",
		"Use tools when needed. Prefer read before edit.",
		"Keep changes within the workspace unless the user asks otherwise.",
	}, "\n")
}

func (b promptBuilder) persona() string {
	if b.env == nil || b.env.Persona == nil {
		return ""
	}
	p := b.env.Persona
	lines := []string{fmt.Sprintf("Persona: %s (id=%s).", blankString(p.Display, p.ID), p.ID)}
	if strings.TrimSpace(p.Description) != "" {
		lines = append(lines, "Role: "+strings.TrimSpace(p.Description))
	}
	lines = append(lines,
		"Stay in character. A turn routed to this persona is an explicit invocation; answer normally.",
		"In Telegram group chats, output exactly NO_REPLY only for unrelated forwarded chatter.",
	)
	return strings.Join(lines, "\n")
}

func blankString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(s)
}

func (b promptBuilder) context() string {
	var lines []string
	if b.env != nil && strings.TrimSpace(b.env.Workspace) != "" {
		lines = append(lines, "Workspace: "+strings.TrimSpace(b.env.Workspace))
	}
	if b.env != nil && strings.TrimSpace(b.env.ProjectContext) != "" {
		lines = append(lines, "Project context:\n"+strings.TrimSpace(b.env.ProjectContext))
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
	if b.env.Persona != nil {
		if v := loadSoulPrompt(b.env.Persona.SoulPath); v != "" {
			return v
		}
	}
	return loadSoulPrompt(b.env.SoulPath)
}

func (b promptBuilder) preferences() string {
	if b.env == nil {
		return ""
	}
	if v := loadSoulPrompt(b.env.PreferencesPath); v != "" {
		return "User preferences:\n" + v
	}
	return ""
}

func (b promptBuilder) permissions() string {
	source := b.source()
	switch {
	case strings.HasPrefix(source, "telegram:"):
		return strings.Join([]string{
			"Tool permissions:",
			"- Telegram context blocks shell and generic network tools.",
			"- Do not use bash/curl/webhook commands for notifications.",
			"- Use notify_bark for Bark notifications when needed.",
		}, "\n")
	case strings.HasPrefix(source, "cron:"):
		return strings.Join([]string{
			"Tool permissions:",
			"- Cron context may run local monitoring commands.",
			"- Cron context blocks shell-based network/webhook commands such as curl/wget.",
			"- Use notify_bark for Bark notifications when needed.",
		}, "\n")
	default:
		return ""
	}
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
		"- [[photo:<url_or_path_or_file_id>]]",
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
