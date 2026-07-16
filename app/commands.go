package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/tool"
)

func (a *App) registerBuiltinCommands() {
	a.RegisterCommand(command.NewFuncCmd("help", "show help", func(ctx context.Context, args []string) (string, error) {
		return listItems("Commands", a.cmds.All(), func(c command.Command) string {
			return "/" + c.Name() + " - " + c.Desc()
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("tools", "list tools", func(ctx context.Context, args []string) (string, error) {
		return listItems("Tools", a.tools.All(), func(t tool.Tool) string {
			return t.Name() + " - " + t.Desc()
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("skills", "list skill cards", func(ctx context.Context, args []string) (string, error) {
		skills := a.SkillDirectory()
		if len(skills) == 0 {
			return "no skills", nil
		}
		return listItems("Skills", skills, func(s SkillDirectoryItem) string {
			return skillListLine(s)
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("skill", "show skill detail: /skill <name>", func(ctx context.Context, args []string) (string, error) {
		if len(args) == 0 {
			return "usage: /skill <name>", nil
		}
		s, ok := a.SkillDetail(strings.Join(args, " "))
		if !ok {
			return "skill not found: " + strings.Join(args, " "), nil
		}
		return skillDetailText(s), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("tasks", "list recent task states", func(ctx context.Context, args []string) (string, error) {
		tasks := a.RecentTaskStates(8)
		if len(tasks) == 0 {
			return "no task state recorded", nil
		}
		return listItems("Task states", tasks, func(t TaskStateSummary) string {
			label := shortID(t.ID) + " [" + t.Status + "] " + t.Title
			state := compactJoin([]string{t.State.Checkpoint, blockersLabel(t.State.Blockers)}, " / ")
			if state == "" {
				return trimLine(label, 120)
			}
			return trimLine(label, 80) + " - " + trimLine(state, 80)
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("approvals", "list recent action proposals", func(ctx context.Context, args []string) (string, error) {
		proposals := a.RecentActionProposals(8)
		if len(proposals) == 0 {
			return "no action proposals recorded", nil
		}
		return listItems("Action proposals", proposals, func(p ActionProposalSummary) string {
			head := compactJoin([]string{p.Result, p.Tool}, " / ")
			body := compactJoin([]string{p.Proposal.Intent, p.Proposal.Target, p.Proposal.Risk}, " / ")
			if body == "" {
				body = p.Source
			}
			if head == "" {
				return trimLine(body, 120)
			}
			return trimLine(head, 56) + " - " + trimLine(body, 100)
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("model", "show or set model: /model [name] or /model <provider> <model>", func(ctx context.Context, args []string) (string, error) {
		if len(args) == 0 {
			return a.currentModel(), nil
		}
		if len(args) > 2 {
			return "usage: /model <configured-name> or /model <provider> <model>\nUse /models to list configured options.", nil
		}
		model := ""
		if len(args) == 2 {
			model = args[1]
		}
		if err := a.switchModel(args[0], model); err != nil {
			return modelSwitchError(err, args[0], model, a.cfg), nil
		}
		msg := "switched model to " + a.currentModel()
		if err := config.PersistModel(a.cfg); err != nil {
			msg += "\nwarning: failed to persist model: " + err.Error()
		}
		return msg, nil
	}))

	a.RegisterCommand(command.NewFuncCmd("models", "list detected model options", func(ctx context.Context, args []string) (string, error) {
		var sections []string
		if configured := configuredModelLines(a.cfg); len(configured) > 0 {
			sections = append(sections, "Configured models:\n"+strings.Join(configured, "\n"))
		}
		if opts := config.Detect(); len(opts) > 0 {
			sections = append(sections, listItems("Detected models", opts, func(opt config.Option) string {
				return opt.Provider + " / " + opt.Model + " [" + opt.Source + "]"
			}))
		}
		if len(sections) == 0 {
			return "no configured model providers detected", nil
		}
		return strings.Join(sections, "\n\n"), nil
	}))

	// NOTE: /session is intentionally NOT registered by core. The sessioncmd
	// plugin (loaded after core) provides a strict superset (adds fork/close and
	// richer list/current formatting), so it is the single authoritative
	// /session implementation. See plugins/sessioncmd/session.go.
	a.RegisterCommand(command.NewFuncCmd("context", "inspect or reset runtime context: /context [inspect|reset-session|reset-summary]", a.runContextCommand))
	a.RegisterCommand(command.NewFuncCmd("compact", "summarize and compact current session", a.runCompactCommand))
	a.RegisterCommand(command.NewFuncCmd("project", "manage project context: /project [view|init|edit|path]", a.runProjectCommand))
	a.RegisterCommand(command.NewFuncCmd("file", "attach a text file to current session: /file <path>", a.runFileCommand))
	a.RegisterCommand(command.NewFuncCmd("usage", "show recorded API token usage", a.runUsageCommand))
}

func configuredModelLines(cfg config.Config) []string {
	if len(cfg.Models) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Models))
	for name := range cfg.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		mc := cfg.Models[name]
		status := "ready"
		if strings.TrimSpace(mc.APIKey) == "" && strings.TrimSpace(cfg.APIKey) == "" {
			status = "missing api key"
		}
		active := ""
		if cfg.ActiveModel == name {
			active = " *"
		}
		lines = append(lines, "- "+name+active+" - "+compactJoin([]string{mc.Provider, mc.Model, status}, " / "))
	}
	return lines
}

func modelSwitchError(err error, provider, model string, cfg config.Config) string {
	target := strings.TrimSpace(provider)
	if strings.TrimSpace(model) != "" {
		target += " / " + strings.TrimSpace(model)
	}
	var b strings.Builder
	b.WriteString("model switch failed: ")
	b.WriteString(err.Error())
	if len(cfg.Models) > 0 {
		b.WriteString("\nUse `/models` to list configured names, then switch with `/model <name>`.")
	} else {
		b.WriteString("\nUse `/models` to list detected providers, or configure a named model first.")
	}
	if target != "" {
		b.WriteString("\nrequested: ")
		b.WriteString(target)
	}
	return b.String()
}

func skillListLine(s SkillDirectoryItem) string {
	status := "ready"
	if !s.Configured {
		status = "missing " + strings.Join(s.MissingEnv, ",")
	}
	if s.LastAction != "" {
		status += " / " + s.LastAction
	}
	meta := compactJoin([]string{s.Risk, s.When}, " / ")
	if meta == "" {
		meta = s.Description
	}
	if meta == "" {
		return s.Name + " [" + status + "]"
	}
	return s.Name + " [" + status + "] - " + trimLine(meta, 100)
}

func skillDetailText(s SkillDirectoryItem) string {
	var lines []string
	lines = append(lines, s.Name)
	if s.Description != "" {
		lines = append(lines, "Description: "+s.Description)
	}
	if s.When != "" {
		lines = append(lines, "When: "+s.When)
	}
	if s.Risk != "" {
		lines = append(lines, "Risk: "+s.Risk)
	}
	if len(s.EnvNeeds) > 0 {
		for _, need := range s.EnvNeeds {
			state := "missing"
			if need.Configured {
				state = "configured"
			}
			line := "Env: " + need.Name + " [" + state + "]"
			if need.Hint != "" && !need.Configured {
				line += " - " + need.Hint
			}
			lines = append(lines, line)
		}
	}
	if len(s.Entrypoints) > 0 {
		lines = append(lines, "Entrypoints: "+strings.Join(s.Entrypoints, ", "))
	}
	if len(s.Examples) > 0 {
		lines = append(lines, "Examples: "+strings.Join(s.Examples, " | "))
	}
	if s.LastAction != "" {
		lines = append(lines, "Recent: "+s.LastAction)
	}
	if s.Body != "" {
		lines = append(lines, "", strings.TrimSpace(s.Body))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (a *App) runCompactCommand(ctx context.Context, args []string) (string, error) {
	source := command.SourceFrom(ctx)
	if a.manualCompactSpaceBacked(source) {
		return "", ErrManualCompactSpaceBacked
	}
	s, err := a.sessions.Current(source)
	if err != nil {
		return "", err
	}
	keep := a.cfg.Compact.KeepRecentMessages
	if keep < 0 {
		keep = 8
	}
	note := strings.TrimSpace(strings.Join(args, " "))
	summary, err := a.compactSessionKeepNote(ctx, s, keep, note)
	if err != nil {
		return "", err
	}
	if err := a.sessions.Save(s); err != nil {
		return "", err
	}
	a.bus.Publish(bus.Event{
		Type:      bus.SessionCompacted,
		Source:    source,
		SessionID: s.ID,
		Text:      summary,
	})
	return "session compacted", nil
}

func listItems[T any](title string, items []T, fn func(T) string) string {
	var b strings.Builder
	b.WriteString(title + ":\n")
	for _, item := range items {
		b.WriteString("  " + fn(item) + "\n")
	}
	return strings.TrimSpace(b.String())
}

func compactJoin(parts []string, sep string) string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func trimLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if n <= 0 || len(runes) <= n {
		return s
	}
	if n <= 3 {
		return "..."
	}
	return string(runes[:n-3]) + "..."
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func blockersLabel(blockers []string) string {
	if len(blockers) == 0 {
		return ""
	}
	if len(blockers) == 1 {
		return "1 blocker"
	}
	return fmt.Sprintf("%d blockers", len(blockers))
}
