package agent

import (
	"fmt"
	"os"
	"path/filepath"
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
	p.Add(b.collaboration())
	p.Add(b.memoryPolicy())
	p.Add(b.taskDelegation())
	p.Add(b.context())
	p.Add(b.personaRuntimeContext())
	p.Add(b.skills())
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

func (b promptBuilder) collaboration() string {
	if b.turn == nil || strings.TrimSpace(b.turn.CollaborationBrief) == "" {
		return ""
	}
	return strings.Join([]string{
		"Collaboration protocol:",
		"- If directly mentioned, respond.",
		"- If joined through listening, respond only when you add value.",
		"- Do not repeat another agent's answer; build on it or correct it.",
		"- If another agent is better suited, mention them and state why.",
		"- Converge toward a decision, answer, or next action.",
		"- Keep cross-agent discussion concise; no greetings or status filler.",
		"",
		"Collaboration brief:",
		strings.TrimSpace(b.turn.CollaborationBrief),
	}, "\n")
}

func (b promptBuilder) memoryPolicy() string {
	if b.env == nil || b.env.Persona == nil {
		return strings.Join([]string{
			"Memory protocol:",
			"- If the current user explicitly says to remember a stable preference or fact, call remember_memory and then reply with a brief Remembered note.",
			"- If you infer a possible long-term memory yourself, call propose_memory instead; do not claim it is saved.",
			"- Do not remember one-off lookups, temporary task state, unverified guesses, debug intermediate state, credentials, tokens, keys, cookies, or webhook URLs.",
		}, "\n")
	}
	if b.env.Persona.MemoryPolicy == "auto_commit" {
		return strings.Join([]string{
			"Memory protocol:",
			"- Current memory policy: auto-commit.",
			"- Only write durable memory for stable preferences, identity facts, project conventions, or confirmed long-lived decisions.",
			"- Do not remember one-off lookups, temporary task state, unverified guesses, debug intermediate state, credentials, tokens, keys, cookies, or webhook URLs.",
		}, "\n")
	}
	return strings.Join([]string{
		"Memory protocol:",
		"- Current memory policy: proposal-only.",
		"- If the current user explicitly says to remember a stable preference, fact, project convention, or long-lived decision, call remember_memory with authorization_text copied from that user message; then reply briefly with Remembered and the undo path.",
		"- If you infer a possible long-term memory from ordinary chat, call propose_memory with scope, kind, content, reason, and confidence; do not claim it is saved.",
		"- Do not call write_memory or delete_memory in proposal-only mode.",
		"- After proposing, tell the user the proposal id and that they can confirm it with !memory confirm <id> or reject it with !memory reject <id>.",
		"- Do not propose memory for one-off lookups, temporary task state, unverified guesses, debug intermediate state, credentials, tokens, keys, cookies, or webhook URLs.",
	}, "\n")
}

func (b promptBuilder) taskDelegation() string {
	if b.env == nil || b.env.Persona == nil {
		return ""
	}
	caps := taskCapabilities(b.env.Persona.Capabilities)
	if len(caps) == 0 {
		return strings.Join([]string{
			"Task delegation protocol:",
			"- This persona has no task.* capabilities.",
			"- Do not create, assign, execute, or review Task Board items.",
			"- If work should become a task, propose the task shape and ask a capable agent or human to create it.",
		}, "\n")
	}
	if b.env.Persona.TaskPolicy != "auto_commit" {
		return strings.Join([]string{
			"Task delegation protocol:",
			"- Current task capabilities: " + strings.Join(caps, ", ") + ".",
			"- Current task policy: propose-only.",
			"- Do not suggest or create Task Board candidates unless the current user explicitly asks to create/record/assign a task.",
			"- Do not create, assign, or update real Task Board items yourself.",
			"- If the user only asks for help, a fix, an explanation, a lookup, or a review, answer or do the work directly without task ceremony.",
			"- A real task commit requires explicit user task intent plus human confirmation through the UI or an explicit auto-commit task policy.",
		}, "\n")
	}
	return strings.Join([]string{
		"Task delegation protocol:",
		"- Current task capabilities: " + strings.Join(caps, ", ") + ".",
		"- Current task policy: auto-commit is enabled.",
		"- A task is a commitment with owner, expected outcome, acceptance criteria, and review/status flow.",
		"- Create or assign a task only when the current user explicitly asks to create/record/assign a task.",
		"- Do not create tasks for simple Q&A, quick lookups, link checks, concept explanations, or ordinary conversation.",
		"- Fix/build/review/deploy requests are not task-creation requests by themselves; do the work directly unless the user says to make it a task.",
		"- task.plan may break work into explicit candidate tasks only when the user asked for task creation; discussion or brainstorming is not a task by itself.",
		"- task.create/task.assign require a clear title, assignee, expected outcome, acceptance criteria, and source.",
		"- If explicit task intent exists but outcome, assignee, acceptance criteria, or source is missing, ask a focused question instead of creating a task.",
		"- If the user only asks to check or explain something once, answer directly without task_create.",
		"- task.execute agents may accept assigned work and move it to in_progress, then in_review when ready.",
		"- task.review agents may mark reviewed work done or closed; executors should not self-done their own work.",
		"- If you lack a required task capability, only suggest the action or mention an agent that has it.",
	}, "\n")
}

func taskCapabilities(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = normalizeTaskCapability(v)
		switch v {
		case "task.plan", "task.create", "task.assign", "task.execute", "task.review":
		default:
			continue
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func normalizeTaskCapability(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", ".")
	s = strings.ReplaceAll(s, ":", ".")
	switch s {
	case "plan":
		return "task.plan"
	case "create":
		return "task.create"
	case "assign":
		return "task.assign"
	case "execute", "exec":
		return "task.execute"
	case "review":
		return "task.review"
	default:
		return s
	}
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
		lines = append(lines, "Historical conversation summary (weak context; prefer current user message and recent transcript for facts):\n"+strings.TrimSpace(s.Summary))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (b promptBuilder) skills() string {
	if b.env == nil || len(b.env.SkillCards) == 0 {
		return ""
	}
	return "Available skills:\n" + strings.Join(b.env.SkillCards, "\n")
}

func (b promptBuilder) soul() string {
	if b.env == nil {
		return ""
	}
	var sections []string
	if raw := loadSoulPrompt(b.env.SoulPath); raw != "" {
		if b.env.Persona == nil {
			sections = append(sections, "Sumi base identity (root SOUL.md):\n"+renderSoulTemplate(raw, b.soulTemplateData()))
		} else if v := inheritableRootSoul(raw); v != "" {
			sections = append(sections, "Sumi base identity (inherited root SOUL.md):\n"+v)
		}
	}
	if b.env.Persona != nil {
		if raw := loadSoulPrompt(b.env.Persona.SoulPath); raw != "" {
			sections = append(sections, "Persona soul overlay (persona SOUL.md):\n"+renderSoulTemplate(raw, b.soulTemplateData()))
		}
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func (b promptBuilder) personaRuntimeContext() string {
	if b.env == nil || b.env.Persona == nil {
		return ""
	}
	var lines []string
	lines = append(lines, "Persona runtime context:")
	lines = append(lines, "- persona_id: "+b.env.Persona.ID)
	if source := b.source(); source != "" {
		lines = append(lines, "- source: "+source)
	}
	if workspace := strings.TrimSpace(b.env.Workspace); workspace != "" {
		lines = append(lines, "- workspace: "+workspace)
	}
	scopes := []string{"persona:" + b.env.Persona.ID}
	if source := b.source(); source != "" {
		scopes = append(scopes, "channel:"+source)
	}
	if workspace := strings.TrimSpace(b.env.Workspace); workspace != "" {
		scopes = append(scopes, "workspace:"+workspace)
	}
	scopes = append(scopes, "global")
	lines = append(lines, "- memory_scopes: "+strings.Join(scopes, ", "))
	return strings.Join(lines, "\n")
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
	case isTelegramPromptSource(source):
		return strings.Join([]string{
			"Tool permissions:",
			"- Telegram context may run configured scripts when needed.",
			"- Do not use raw curl/webhook commands for notifications.",
			"- Use configured skills for notifications when needed.",
		}, "\n")
	case strings.HasPrefix(source, "cron:"):
		return strings.Join([]string{
			"Tool permissions:",
			"- Cron context may run configured monitoring scripts when needed.",
			"- Do not use raw curl/webhook commands for notifications.",
			"- Use configured skills for notifications when needed.",
		}, "\n")
	default:
		return ""
	}
}

func (b promptBuilder) telegram() string {
	if !isTelegramPromptSource(b.source()) {
		return ""
	}
	scope := normalizeTelegramScope(b.env)
	scopeLine := "- Session scope: chat-wide context."
	if scope == "thread" {
		scopeLine = "- Session scope: per-thread context when thread_id exists."
	}
	return strings.Join([]string{
		"You operate inside Telegram chats. Allowed chat messages are forwarded directly to you.",
		"",
		"Behavior:",
		"- Group delivery mode: allowed group messages use the same default direct conversation path as private chats.",
		"- Treat @names in Telegram text as normal user content unless they are Telegram-specific reply instructions.",
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

func isTelegramPromptSource(source string) bool {
	source = strings.TrimSpace(source)
	return strings.HasPrefix(source, "tg:") || strings.HasPrefix(source, "telegram:")
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

type soulTemplateData struct {
	Workspace       string
	MemoryRoot      string
	Source          string
	PersonaID       string
	PersonaSoulPath string
	PersonaRoot     string
}

func (b promptBuilder) soulTemplateData() soulTemplateData {
	var d soulTemplateData
	if b.env != nil {
		d.Workspace = strings.TrimSpace(b.env.Workspace)
		d.MemoryRoot = strings.TrimSpace(b.env.MemoryRoot)
		d.Source = b.source()
		if b.env.Persona != nil {
			d.PersonaID = strings.TrimSpace(b.env.Persona.ID)
			d.PersonaSoulPath = strings.TrimSpace(b.env.Persona.SoulPath)
			if d.PersonaSoulPath != "" {
				d.PersonaRoot = filepath.Dir(d.PersonaSoulPath)
			}
		}
	}
	return d
}

func inheritableRootSoul(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var out []string
	seenHeading := false
	keepSection := true
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if isMarkdownHeading(trimmed) {
			seenHeading = true
			keepSection = inheritableRootSoulHeading(trimmed)
			if !keepSection {
				continue
			}
			out = append(out, line)
			continue
		}
		if seenHeading && !keepSection {
			continue
		}
		if rootPrivateInheritedSoulLine(trimmed) {
			continue
		}
		if strings.Contains(trimmed, "{{") && strings.Contains(trimmed, "}}") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(cleanBlankLines(out))
}

func renderSoulTemplate(raw string, data soulTemplateData) string {
	replacements := map[string]string{
		"{{workspace}}":         data.Workspace,
		"{{memory_root}}":       data.MemoryRoot,
		"{{source}}":            data.Source,
		"{{persona_id}}":        data.PersonaID,
		"{{persona_soul_path}}": data.PersonaSoulPath,
		"{{persona_root}}":      data.PersonaRoot,
	}
	out := raw
	for from, to := range replacements {
		out = strings.ReplaceAll(out, from, to)
	}
	return strings.TrimSpace(out)
}

func isMarkdownHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	return len(line) == 1 || line[1] == ' ' || line[1] == '#'
}

func inheritableRootSoulHeading(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	title := strings.TrimSpace(strings.TrimLeft(lower, "#"))
	title = strings.TrimSpace(strings.TrimSuffix(title, ":"))
	if title == "" {
		return true
	}
	allowed := map[string]bool{
		"soul.md - who you are":        true,
		"soul.md - sumi base identity": true,
		"core truths":                  true,
		"inheritable identity":         true,
		"boundaries":                   true,
		"universal boundaries":         true,
		"working style":                true,
		"universal working style":      true,
	}
	return allowed[title]
}

func rootPrivateInheritedSoulLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	markers := []string{
		"memory.md",
		"memory path",
		"memory root",
		"memory directory",
		"memory dir",
		"memory/",
		"runtime path",
		"workspace path",
		"workspace root",
		"working directory",
		"relative path",
		"root-private",
		"self-maintenance",
		"self maintenance",
		"self directory",
		"ledger.md",
		"daily memory",
		"~/.sumi",
		"$home",
		"self/",
		"personas/",
		"session/",
		"sessions/",
		"runlog/",
		"state/",
		"记忆路径",
		"记忆目录",
		"工作目录",
		"相对路径",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func cleanBlankLines(lines []string) string {
	var out []string
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
