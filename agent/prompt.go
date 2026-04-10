package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/memory"
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

func (a *Agent) buildPrompt(ctx context.Context, src string) string {
	sections := []section{
		a.sectionBase(),
		a.sectionContext(),
		a.sectionTeam(ctx),
		a.sectionMemory(ctx, src),
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

func (a *Agent) sectionMemory(ctx context.Context, src string) section {
	return section{head: "Memory", body: func() string {
		if a.mem == nil {
			return ""
		}
		var b strings.Builder
		appendDocs := func(prefix string, docs []memory.Doc, err error) {
			if err != nil {
				return
			}
			for _, doc := range docs {
				line := strings.TrimSpace(doc.Summary)
				if line == "" {
					line = strings.TrimSpace(doc.Body)
				}
				if line == "" {
					line = strings.TrimSpace(doc.Title)
				}
				if line == "" {
					continue
				}
				if prefix == "" {
					fmt.Fprintf(&b, "- %s\n", line)
					continue
				}
				fmt.Fprintf(&b, "- %s: %s\n", prefix, line)
			}
		}
		docs, err := a.mem.RecentByScope(ctx, memory.GlobalScope(), 2)
		appendDocs("global", docs, err)
		if a.rt != nil {
			docs, err = a.mem.RecentByScope(ctx, memory.WorkspaceScope(a.rt.WorkspaceID()), 3)
			appendDocs("workspace", docs, err)
		}
		if src != "" {
			docs, err = a.mem.RecentByScope(ctx, memory.ChannelScope(src), 3)
			appendDocs("channel", docs, err)
		}
		docs, err = a.mem.RecentByScope(ctx, memory.AgentScope(a.id), 3)
		appendDocs("agent", docs, err)
		turn, ok := runtimeTurnFrom(ctx)
		if ok && turn.TaskID != "" {
			docs, err = a.mem.RecentByTask(ctx, turn.TaskID, 3)
			appendDocs("task", docs, err)
		}
		if teamTurn, ok := teamTurnFrom(ctx); ok && teamTurn.TeamID != "" {
			docs, err = a.mem.RecentByScope(ctx, memory.TeamScope(teamTurn.TeamID), 3)
			appendDocs("team", docs, err)
		}
		if b.Len() == 0 {
			return ""
		}
		return b.String()
	}}
}

func (a *Agent) sectionTeam(ctx context.Context) section {
	return section{head: "Team", body: func() string {
		turn, ok := teamTurnFrom(ctx)
		if !ok || turn.TeamID == "" {
			return ""
		}
		var lines []string
		lines = append(lines, "You are speaking inside a persistent team thread.")
		lines = append(lines, fmt.Sprintf("- Team ID: %s", turn.TeamID))
		lines = append(lines, fmt.Sprintf("- Thread ID: %s", turn.ThreadID))
		lines = append(lines, fmt.Sprintf("- Current speaker identity: %s", turn.SpeakerAgentID))
		if turn.SpeakerRole != "" {
			lines = append(lines, fmt.Sprintf("- Current speaker role: %s", turn.SpeakerRole))
		}
		if turn.SpeakerRoleDesc != "" {
			lines = append(lines, fmt.Sprintf("- Current speaker scope: %s", turn.SpeakerRoleDesc))
		}
		if turn.SpeakerProfile != "" {
			lines = append(lines, fmt.Sprintf("- Current speaker profile: %s", turn.SpeakerProfile))
		}
		if turn.Goal != "" {
			lines = append(lines, fmt.Sprintf("- Current thread goal: %s", turn.Goal))
		}
		if turn.MaxRounds > 0 {
			lines = append(lines, fmt.Sprintf("- Team round: %d/%d", turn.Round, turn.MaxRounds))
		}
		if prompt := strings.TrimSpace(turn.Prompt); prompt != "" {
			lines = append(lines, fmt.Sprintf("- Local turn directive: %s", prompt))
		}
		lines = append(lines, "- If you schedule another speaker with mention, stop after the handoff instead of emitting the final answer yourself.")
		return strings.Join(lines, "\n")
	}}
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
	return section{head: "Persona", body: func() string {
		return loadSoulPrompt(a.soulPath)
	}}
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

func loadSoulPrompt(path string) string {
	if strings.TrimSpace(path) == "" {
		path = config.SoulPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
