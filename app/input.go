package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

func (a *App) HandleInput(ctx context.Context, source, input string) (string, error) {
	return a.handleInput(ctx, source, "", a.cfg.Runtime, input, nil)
}

func (a *App) HandleInputWithRuntime(ctx context.Context, source, runtime, input string) (string, error) {
	return a.handleInput(ctx, source, "", runtime, input, nil)
}

func (a *App) HandleInputWithAttachments(ctx context.Context, source, input string, attachments []msg.Attachment) (string, error) {
	return a.handleInput(ctx, source, "", a.cfg.Runtime, input, attachments)
}

func (a *App) HandleInputAs(ctx context.Context, source, personaID, input string) (string, error) {
	return a.handleInputAs(ctx, source, personaID, input, nil)
}

func (a *App) handleInputAs(ctx context.Context, source, personaID, input string, attachments []msg.Attachment) (string, error) {
	p := a.personas.Get(personaID)
	if p == nil {
		return "", fmt.Errorf("persona not found: %s", personaID)
	}
	rt := p.Runtime
	if rt == "" {
		rt = a.cfg.Runtime
	}
	return a.handleInput(ctx, source, p.ID, rt, input, attachments)
}

func (a *App) handleInput(ctx context.Context, source, personaID, runtime, input string, attachments []msg.Attachment) (string, error) {
	return inputFlow{
		app:         a,
		source:      source,
		personaID:   personaID,
		runtime:     runtime,
		input:       input,
		attachments: attachments,
	}.run(ctx)
}

type inputFlow struct {
	app         *App
	source      string
	personaID   string
	runtime     string
	input       string
	attachments []msg.Attachment
}

func (f inputFlow) run(ctx context.Context) (string, error) {
	ctx = command.WithSource(ctx, f.source)
	if f.personaID != "" {
		ctx = command.WithPersona(ctx, f.personaID)
	}
	if out, ok, err := f.route(ctx); ok {
		return out, err
	}
	input, attachments, err := prepareImageInput(f.input, f.attachments)
	if err != nil {
		return "", err
	}
	f.input = input
	f.attachments = attachments
	if f.personaID == "" && sourceUsesRouter(f.source) {
		if _, err := f.app.interceptRoutedInput(ctx, f.source, f.input); err != nil {
			return "", err
		}
		return "", nil
	}
	if out, ok, err := f.mention(ctx); ok {
		return out, err
	}
	if space.MapSource(f.source).Kind == space.KindAgentDM {
		personaID, _, err := f.app.resolveAgentDMPersonaID(f.source, f.personaID)
		if err != nil {
			return "", err
		}
		f.personaID = personaID
		ctx = command.WithPersona(ctx, personaID)
		if _, err := f.app.appendAgentDMUserToSpace(f.source, personaID, f.input); err != nil {
			return "", err
		}
	}
	sessionSource := f.sessionSource()
	s, err := f.app.sessions.Current(sessionSource)
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
	rt, err := f.app.newRuntimeFor(f.runtime, f.app.personas.Get(f.personaID))
	if err != nil {
		return "", err
	}
	if err := f.app.runTurnAs(ctx, rt, f.source, f.personaID, f.input, f.attachments, s); err != nil {
		return "", err
	}
	return latestAssistant(s), nil
}

func (f inputFlow) sessionSource() string {
	return personaSessionSource(f.source, f.personaID)
}

func personaSessionSource(source, personaID string) string {
	source = strings.TrimSpace(source)
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return source
	}
	if source == "" {
		source = "default"
	}
	return source + ":persona:" + personaID
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

func (f inputFlow) mention(ctx context.Context) (string, bool, error) {
	if f.personaID != "" {
		return "", false, nil
	}
	id, rest, ok := parseMention(f.input)
	if !ok {
		return "", false, nil
	}
	p := f.app.personas.Get(id)
	if p == nil {
		return "", false, nil
	}
	out, err := f.app.handleInputAs(ctx, f.source, p.ID, rest, f.attachments)
	return out, true, err
}

func parseMention(input string) (string, string, bool) {
	s := strings.TrimSpace(input)
	if !strings.HasPrefix(s, "@") {
		return "", "", false
	}
	rest := s[1:]
	idEnd := 0
	for idEnd < len(rest) {
		c := rest[idEnd]
		if c == ' ' || c == '\t' || c == '\n' {
			break
		}
		idEnd++
	}
	id := rest[:idEnd]
	body := strings.TrimSpace(rest[idEnd:])
	if id == "" {
		return "", "", false
	}
	return id, body, true
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
