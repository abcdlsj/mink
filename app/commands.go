package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/tool"
)

func (a *App) registerBuiltinCommands() {
	a.RegisterCommand(command.NewFuncCmd("help", "show help", func(ctx context.Context, args []string) (string, error) {
		return listItems("Commands", a.cmds.All(), func(c command.Command) string {
			return "!" + c.Name() + " - " + c.Desc()
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("tools", "list tools", func(ctx context.Context, args []string) (string, error) {
		return listItems("Tools", a.tools.All(), func(t tool.Tool) string {
			return t.Name() + " - " + t.Desc()
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("model", "show or set model: !model [provider model]", func(ctx context.Context, args []string) (string, error) {
		if len(args) == 0 {
			return a.currentModel(), nil
		}
		if len(args) != 2 {
			return "usage: !model <provider> <model>", nil
		}
		if err := a.switchModel(args[0], args[1]); err != nil {
			return "", err
		}
		return "switched model to " + a.currentModel(), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("models", "list detected model options", func(ctx context.Context, args []string) (string, error) {
		opts := config.Detect()
		if len(opts) == 0 {
			return "no configured model providers detected", nil
		}
		return listItems("Detected models", opts, func(opt config.Option) string {
			return opt.Provider + " / " + opt.Model + " [" + opt.Source + "]"
		}), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("session", "manage sessions", a.runSessionCommand))
	a.RegisterCommand(command.NewFuncCmd("compact", "summarize and compact current session", a.runCompactCommand))
}

func (a *App) runSessionCommand(ctx context.Context, args []string) (string, error) {
	source := command.SourceFrom(ctx)
	if len(args) == 0 {
		return "usage: !session [list|current|new|switch <id>]", nil
	}
	switch args[0] {
	case "list":
		sessions, err := a.sessions.List()
		if err != nil {
			return "", err
		}
		if len(sessions) == 0 {
			return "no sessions", nil
		}
		var lines []string
		for _, s := range sessions {
			line := s.ID
			if s.Title != "" {
				line += " [" + s.Title + "]"
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case "current":
		s, err := a.sessions.Current(source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s [%s]", s.ID, s.Title), nil
	case "new":
		s, err := a.sessions.New(source)
		if err != nil {
			return "", err
		}
		return "new session: " + s.ID, nil
	case "switch":
		if len(args) < 2 {
			return "usage: !session switch <id>", nil
		}
		s, err := a.sessions.Switch(source, args[1])
		if err != nil {
			return "", err
		}
		return "switched session: " + s.ID, nil
	default:
		return "usage: !session [list|current|new|switch <id>]", nil
	}
}

func (a *App) runCompactCommand(ctx context.Context, args []string) (string, error) {
	source := command.SourceFrom(ctx)
	s, err := a.sessions.Current(source)
	if err != nil {
		return "", err
	}
	summary, err := a.compactSession(ctx, s)
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
	return "compacted session: " + summary, nil
}

func listItems[T any](title string, items []T, fn func(T) string) string {
	var b strings.Builder
	b.WriteString(title + ":\n")
	for _, item := range items {
		b.WriteString("  " + fn(item) + "\n")
	}
	return strings.TrimSpace(b.String())
}
