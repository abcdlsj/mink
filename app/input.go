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
	"github.com/abcdlsj/sumi/session"
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

func (a *App) HandleExistingUserInput(ctx context.Context, source, personaID, input, existingUserMessageID string) (string, error) {
	runtime := a.cfg.Runtime
	if personaID != "" {
		p := a.personas.Get(personaID)
		if p == nil {
			return "", fmt.Errorf("persona not found: %s", personaID)
		}
		runtime = p.Runtime
		if runtime == "" {
			runtime = a.cfg.Runtime
		}
	}
	return inputFlow{
		app:                   a,
		source:                source,
		personaID:             personaID,
		runtime:               runtime,
		input:                 input,
		existingUserMessageID: strings.TrimSpace(existingUserMessageID),
	}.run(ctx)
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
	app                   *App
	source                string
	personaID             string
	runtime               string
	input                 string
	attachments           []msg.Attachment
	existingUserMessageID string
}

func (f inputFlow) run(ctx context.Context) (string, error) {
	policy := command.EntrypointPolicy(f.source)
	ctx = command.WithSource(ctx, f.source)
	ctx = f.withRunContext(ctx)
	if f.personaID != "" {
		ctx = command.WithPersona(ctx, f.personaID)
		ctx = f.withRunContext(ctx)
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
	if f.personaID == "" && policy.Mode == command.ModeDirect && policy.Mention == command.MentionText {
		return f.directConversation(ctx)
	}
	if f.personaID == "" && policy.Mode == command.ModeRouted {
		if _, err := f.app.interceptRoutedInput(ctx, f.source, f.input); err != nil {
			return "", err
		}
		return "", nil
	}
	if policy.Mention == command.MentionLeading {
		if out, ok, err := f.mention(ctx); ok {
			return out, err
		}
	}
	var contextSpaceID, excludeMessageID string
	if space.MapSource(f.source).Kind == space.KindAgentDM {
		personaID, _, err := f.app.resolveAgentDMPersonaID(f.source, f.personaID)
		if err != nil {
			return "", err
		}
		f.personaID = personaID
		if p := f.app.personas.Get(personaID); p != nil && strings.TrimSpace(p.Runtime) != "" {
			f.runtime = p.Runtime
		}
		ctx = command.WithPersona(ctx, personaID)
		ctx = f.withRunContext(ctx)
		if f.existingUserMessageID != "" {
			sp, _, err := f.app.resolveAgentDMTargetSpace(f.source, personaID)
			if err != nil {
				return "", err
			}
			contextSpaceID = sp.ID
			excludeMessageID = f.existingUserMessageID
		} else {
			m, err := f.app.appendAgentDMUserToSpace(f.source, personaID, f.input)
			if err != nil {
				return "", err
			}
			if m != nil {
				contextSpaceID = m.SpaceID
				excludeMessageID = m.ID
			}
		}
	}
	sessionSource := command.SessionSourceFrom(ctx)
	s, err := f.app.sessions.Current(sessionSource)
	if err != nil {
		return "", err
	}
	f.seedDirectContext(ctx, s, contextSpaceID, f.personaID, excludeMessageID)
	release := f.app.sessions.AcquireTurn(s.ID, func(int) {
		f.app.bus.Publish(bus.Event{
			Type:      bus.TurnQueued,
			Source:    f.source,
			SessionID: s.ID,
		})
	})
	defer release()
	runtimeName := runtimeForPermission(f.runtime, command.PermissionFrom(ctx))
	if err := f.app.autoCompact(ctx, f.source, runtimeName, s); err != nil {
		return "", err
	}
	rt, err := f.app.newRuntimeFor(runtimeName, f.app.personas.Get(f.personaID))
	if err != nil {
		return "", err
	}
	if contextSpaceID != "" {
		if err := f.app.runTurnAsWithSpaceHistory(ctx, rt, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	} else {
		if err := f.app.runTurnAs(ctx, rt, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	}
	return latestAssistant(s), nil
}

func (f inputFlow) directConversation(ctx context.Context) (string, error) {
	persona := f.app.defaultPersona()
	agentInfo := space.PersonaInfo{ID: "assistant", Display: "Sumi"}
	useDefaultSumi := isDefaultSumiSource(f.source)
	if persona != nil && !useDefaultSumi {
		f.personaID = persona.ID
		if strings.TrimSpace(persona.Runtime) != "" {
			f.runtime = persona.Runtime
		}
		agentInfo = space.PersonaInfo{ID: persona.ID, Display: persona.Display, Role: persona.Description}
		ctx = command.WithPersona(ctx, persona.ID)
	}
	ctx = command.WithRunContext(ctx, f.runContextWithSession(ctx, strings.TrimSpace(f.source)))

	var sp *space.Space
	var excludeMessageID string
	if f.app.spaces != nil {
		var err error
		sp, err = f.app.spaces.Resolve(f.source, agentInfo)
		if err != nil {
			return "", err
		}
		if f.existingUserMessageID != "" {
			excludeMessageID = f.existingUserMessageID
		} else {
			m, err := f.app.spaces.AppendUserMessageInThread(sp.ID, command.ParentMessageFrom(ctx), f.input, nil)
			if err != nil {
				return "", err
			}
			excludeMessageID = m.ID
		}
	}

	sessionSource := command.SessionSourceFrom(ctx)
	s, err := f.app.sessions.Current(sessionSource)
	if err != nil {
		return "", err
	}
	if sp != nil {
		f.seedDirectContext(ctx, s, sp.ID, agentInfo.ID, excludeMessageID)
	}
	release := f.app.sessions.AcquireTurn(s.ID, func(int) {
		f.app.bus.Publish(bus.Event{
			Type:      bus.TurnQueued,
			Source:    f.source,
			SessionID: s.ID,
		})
	})
	defer release()
	runtimeName := runtimeForPermission(f.runtime, command.PermissionFrom(ctx))
	if err := f.app.autoCompact(ctx, f.source, runtimeName, s); err != nil {
		return "", err
	}
	baseline := len(s.Messages)
	runtimePersona := persona
	if useDefaultSumi {
		runtimePersona = nil
	}
	rt, err := f.app.newRuntimeFor(runtimeName, runtimePersona)
	if err != nil {
		return "", err
	}
	if sp != nil {
		if err := f.app.runTurnAsWithSpaceHistory(ctx, rt, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	} else {
		if err := f.app.runTurnAs(ctx, rt, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	}
	content, reasoning := msg.AssistantOutput(s.Messages[baseline:])
	if sp != nil && (strings.TrimSpace(content) != "" || strings.TrimSpace(reasoning) != "") {
		draft := space.Message{
			AuthorID:    agentInfo.ID,
			AuthorKind:  space.ParticipantAgent,
			Content:     content,
			Reasoning:   reasoning,
			Usage:       msg.AssistantUsage(s.Messages[baseline:]),
			RuntimeMeta: msg.AssistantRuntimeMeta(s.Messages[baseline:]),
		}
		_, _, err := f.app.spaces.AppendMessageWithRouting(sp.ID, draft, nil, nil)
		if err != nil {
			return "", err
		}
	}
	return content, nil
}

func isDefaultSumiSource(source string) bool {
	return strings.TrimSpace(source) == "desktop"
}

func (f inputFlow) seedDirectContext(ctx context.Context, s *session.Session, spaceID, agentID, excludeMessageID string) {
	if strings.TrimSpace(spaceID) == "" || strings.TrimSpace(agentID) == "" {
		return
	}
	f.app.BuildContextView(ContextViewInput{
		SpaceID:          spaceID,
		Source:           f.source,
		ParentMessageID:  command.ParentMessageFrom(ctx),
		AgentID:          agentID,
		ExcludeMessageID: excludeMessageID,
	}).Apply(s)
}

func runtimeForPermission(runtime, permission string) string {
	runtime = strings.TrimSpace(runtime)
	switch strings.TrimSpace(strings.ToLower(permission)) {
	case "telegram", "cron":
		switch strings.ToLower(runtime) {
		case "claude", "codex":
			return "native"
		}
	}
	return runtime
}

func personaSessionSource(source, personaID string) string {
	return command.PersonaSessionSource(source, personaID)
}

func (f inputFlow) withRunContext(ctx context.Context) context.Context {
	return command.WithRunContext(ctx, f.runContext(ctx))
}

func (f inputFlow) runContext(ctx context.Context) command.RunContext {
	src := strings.TrimSpace(f.source)
	policy := command.EntrypointPolicy(src)
	return f.runContextWithSession(ctx, policy.SessionSource(src, f.personaID))
}

func (f inputFlow) runContextWithSession(ctx context.Context, session string) command.RunContext {
	src := strings.TrimSpace(f.source)
	delivery := command.NoticeSourceFrom(ctx)
	if strings.TrimSpace(delivery) == "" {
		delivery = src
	}
	return command.RunContext{
		Source:     src,
		Session:    strings.TrimSpace(session),
		Delivery:   strings.TrimSpace(delivery),
		Memory:     f.memoryScopes(src, delivery),
		Permission: command.EntrypointPolicy(src).Permission,
		Input:      f.input,
	}
}

func (f inputFlow) memoryScopes(source, delivery string) []command.MemoryScope {
	var out []command.MemoryScope
	if strings.TrimSpace(f.personaID) != "" {
		out = append(out, command.MemoryScope{Kind: "persona", Key: strings.TrimSpace(f.personaID)})
	}
	if strings.TrimSpace(source) != "" {
		out = append(out, command.MemoryScope{Kind: "channel", Key: strings.TrimSpace(source)})
	}
	if d := strings.TrimSpace(delivery); d != "" && d != strings.TrimSpace(source) {
		out = append(out, command.MemoryScope{Kind: "channel", Key: d})
	}
	if strings.TrimSpace(f.app.cfg.Workspace) != "" {
		out = append(out, command.MemoryScope{Kind: "workspace", Key: strings.TrimSpace(f.app.cfg.Workspace)})
	}
	out = append(out, command.MemoryScope{Kind: "global", Key: ""})
	return out
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
