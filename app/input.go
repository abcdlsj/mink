package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
)

func (a *App) HandleInput(ctx context.Context, source, input string) (string, error) {
	return a.handleInput(ctx, source, a.cfg.Runtime, input)
}

func (a *App) HandleInputWithRuntime(ctx context.Context, source, runtime, input string) (string, error) {
	return a.handleInput(ctx, source, runtime, input)
}

func (a *App) handleInput(ctx context.Context, source, runtime, input string) (string, error) {
	return inputFlow{
		app:     a,
		source:  source,
		runtime: runtime,
		input:   input,
	}.run(ctx)
}

type inputFlow struct {
	app     *App
	source  string
	runtime string
	input   string
}

func (f inputFlow) run(ctx context.Context) (string, error) {
	ctx = command.WithSource(ctx, f.source)
	if out, ok, err := f.route(ctx); ok {
		return out, err
	}
	s, err := f.app.sessions.Current(f.source)
	if err != nil {
		return "", err
	}
	release := f.app.sessions.AcquireTurn(s.ID, func(int) {
		f.app.bus.Publish(bus.Event{
			Type:      bus.TurnQueued,
			Source:    f.source,
			SessionID: s.ID,
		})
	})
	defer release()
	if err := f.app.autoCompact(ctx, f.source, f.runtime, s); err != nil {
		return "", err
	}
	rt, err := f.app.newRuntime(f.runtime)
	if err != nil {
		return "", err
	}
	if err := f.app.runTurn(ctx, rt, f.source, f.input, s); err != nil {
		return "", err
	}
	return latestAssistant(s), nil
}

func (f inputFlow) route(ctx context.Context) (string, bool, error) {
	if out, ok, err := f.app.router.Route(ctx, f.input); ok {
		f.publishCommandHandled(out, err)
		return out, true, err
	}
	if !command.IsCommand(f.input) {
		return "", false, nil
	}
	out, ok, err := f.runShellShortcut(ctx)
	if ok {
		f.publishCommandHandled(out, err)
	}
	return out, ok, err
}

func (f inputFlow) publishCommandHandled(out string, err error) {
	f.app.bus.Publish(bus.Event{
		Type:   bus.CommandHandled,
		Source: f.source,
		Text:   strings.TrimSpace(out),
		Err:    errString(err),
	})
}

func (f inputFlow) runShellShortcut(ctx context.Context) (string, bool, error) {
	input := strings.TrimSpace(f.input)
	if !strings.HasPrefix(input, "!") {
		return "", false, nil
	}
	cmd := strings.TrimSpace(strings.TrimPrefix(input, "!"))
	if cmd == "" || f.app.tools.Get("bash") == nil {
		return "", false, nil
	}
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	ev := bus.Event{
		Source:     f.source,
		ToolCallID: uuid.New().String()[:8],
		Tool:       "bash",
		Input:      string(args),
	}
	f.app.bus.Publish(shellEvent(ev, bus.ToolCallStarted, "", ""))
	out, err := f.app.tools.Run(ctx, "bash", args)
	if err != nil {
		f.app.bus.Publish(shellEvent(ev, bus.ToolCallFailed, out, err.Error()))
		return out, true, err
	}
	f.app.bus.Publish(shellEvent(ev, bus.ToolCallFinished, out, ""))
	return out, true, nil
}

func shellEvent(base bus.Event, typ, out, err string) bus.Event {
	base.Type = typ
	base.Output = out
	base.Err = err
	return base
}
