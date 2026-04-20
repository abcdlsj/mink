package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
)

func (a *App) HandleInput(ctx context.Context, source, input string) (string, error) {
	return a.handleInput(ctx, source, a.cfg.Runtime, input)
}

func (a *App) HandleInputWithRuntime(ctx context.Context, source, runtime, input string) (string, error) {
	return a.handleInput(ctx, source, runtime, input)
}

func (a *App) handleInput(ctx context.Context, source, runtime, input string) (string, error) {
	ctx = command.WithSource(ctx, source)
	if out, ok, err := a.routeInput(ctx, source, input); ok {
		return out, err
	}
	s, err := a.sessions.Current(source)
	if err != nil {
		return "", err
	}
	if err := a.autoCompact(ctx, source, runtime, s); err != nil {
		return "", err
	}
	rt, err := a.newRuntime(runtime)
	if err != nil {
		return "", err
	}
	if err := a.runTurn(ctx, rt, source, input, s); err != nil {
		return "", err
	}
	return latestAssistant(s), nil
}

func (a *App) routeInput(ctx context.Context, source, input string) (string, bool, error) {
	if out, ok, err := a.router.Route(ctx, input); ok {
		a.publishCommandHandled(source, out, err)
		return out, true, err
	}
	if !command.IsCommand(input) {
		return "", false, nil
	}
	out, ok, err := a.runShellShortcut(ctx, source, input)
	if ok {
		a.publishCommandHandled(source, out, err)
	}
	return out, ok, err
}

func (a *App) publishCommandHandled(source, out string, err error) {
	a.bus.Publish(bus.Event{
		Type:   bus.CommandHandled,
		Source: source,
		Text:   strings.TrimSpace(out),
		Err:    errString(err),
	})
}

func (a *App) runShellShortcut(ctx context.Context, source, input string) (string, bool, error) {
	cmd := strings.TrimSpace(strings.TrimPrefix(input, "!"))
	if cmd == "" || a.tools.Get("bash") == nil {
		return "", false, nil
	}
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	ev := bus.Event{
		Source:     source,
		ToolCallID: uuid.New().String()[:8],
		Tool:       "bash",
		Input:      string(args),
	}
	a.bus.Publish(shellEvent(ev, bus.ToolCallStarted, "", ""))
	out, err := a.tools.Run(ctx, "bash", args)
	if err != nil {
		a.bus.Publish(shellEvent(ev, bus.ToolCallFailed, out, err.Error()))
		return out, true, err
	}
	a.bus.Publish(shellEvent(ev, bus.ToolCallFinished, out, ""))
	return out, true, nil
}

func shellEvent(base bus.Event, typ, out, err string) bus.Event {
	base.Type = typ
	base.Output = out
	base.Err = err
	return base
}
