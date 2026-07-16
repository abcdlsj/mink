package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/agent"
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

func (a *App) HandleInputAsWithAttachments(ctx context.Context, source, personaID, input string, attachments []msg.Attachment) (string, error) {
	return a.handleInputAs(ctx, source, personaID, input, attachments)
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
	prepared, err := f.withPreparedInput()
	if err != nil {
		return "", err
	}
	return EntrypointHandler{flow: prepared, policy: policy}.Dispatch(ctx)
}

func (f inputFlow) withPreparedInput() (inputFlow, error) {
	input, attachments, err := prepareImageInput(f.input, f.attachments)
	if err != nil {
		return inputFlow{}, err
	}
	f.input = input
	f.attachments = attachments
	return f, nil
}

type EntrypointHandler struct {
	flow   inputFlow
	policy command.Entrypoint
}

func (h EntrypointHandler) Dispatch(ctx context.Context) (string, error) {
	f := h.flow
	if f.personaID == "" && h.policy.Mode == command.ModeDirect && h.policy.Mention == command.MentionText {
		return f.directConversation(ctx)
	}
	if f.personaID == "" && h.policy.Mode == command.ModeRouted && f.usesLegacyDirectAssistant() {
		return f.directConversation(ctx)
	}
	if f.personaID == "" && h.policy.Mode == command.ModeRouted {
		return f.routedConversation(ctx)
	}
	if h.policy.Mention == command.MentionLeading {
		if out, ok, err := f.mention(ctx); ok {
			return out, err
		}
	}
	if f.isAgentDM() {
		return f.agentDMConversation(ctx)
	}
	return f.runDefaultTurn(ctx, turnContextSeed{})
}

func (f inputFlow) routedConversation(ctx context.Context) (string, error) {
	if _, err := f.app.interceptRoutedInput(ctx, f.source, f.input, f.attachments); err != nil {
		return "", err
	}
	return "", nil
}

type turnContextSeed struct {
	spaceID          string
	excludeMessageID string
}

func (f inputFlow) isAgentDM() bool {
	return space.MapSource(f.source).Kind == space.KindAgentDM
}

func (f inputFlow) agentDMConversation(ctx context.Context) (string, error) {
	seed, ctx, err := f.prepareAgentDMContext(ctx)
	if err != nil {
		return "", err
	}
	return f.runDefaultTurn(ctx, seed)
}

func (f inputFlow) runDefaultTurn(ctx context.Context, seed turnContextSeed) (string, error) {
	contextSpaceID, excludeMessageID := seed.spaceID, seed.excludeMessageID
	sessionSource := command.SessionSourceFrom(ctx)
	s, err := f.app.sessions.Current(sessionSource)
	if err != nil {
		return "", err
	}
	view := f.seedDirectContext(ctx, s, contextSpaceID, f.personaID, excludeMessageID)
	release := f.app.sessions.AcquireTurn(s.ID, func(int) {
		f.app.bus.Publish(bus.Event{
			Type:      bus.TurnQueued,
			Source:    f.source,
			SessionID: s.ID,
		})
	})
	defer release()
	runtimeName := runtimeForPermission(f.runtime, command.PermissionFrom(ctx))
	if err := f.app.autoCompact(ctx, f.source, runtimeName, s, view, f.input, f.attachments); err != nil {
		return "", err
	}
	rt, visionLabel, err := f.app.newRuntimeForTurn(runtimeName, f.app.personas.Get(f.personaID), f.attachments)
	if err != nil {
		return "", err
	}
	f.notifyVisionRoute(visionLabel)
	if contextSpaceID != "" {
		if err := f.app.runTurnAsWithSpaceHistoryNamed(ctx, rt, runtimeName, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	} else {
		if err := f.app.runTurnAsNamed(ctx, rt, runtimeName, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	}
	if visionLabel != "" {
		agent.StripVisionedImageAttachments(s)
	}
	return latestAssistant(s), nil
}

func (f *inputFlow) prepareAgentDMContext(ctx context.Context) (turnContextSeed, context.Context, error) {
	personaID, _, err := f.app.resolveAgentDMPersonaID(f.source, f.personaID)
	if err != nil {
		return turnContextSeed{}, ctx, err
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
			return turnContextSeed{}, ctx, err
		}
		return turnContextSeed{spaceID: sp.ID, excludeMessageID: f.existingUserMessageID}, ctx, nil
	}
	m, err := f.app.appendAgentDMUserWithAttachmentsToSpace(f.source, personaID, f.input, f.attachments)
	if err != nil {
		return turnContextSeed{}, ctx, err
	}
	if m != nil {
		return turnContextSeed{spaceID: m.SpaceID, excludeMessageID: m.ID}, ctx, nil
	}
	return turnContextSeed{}, ctx, nil
}

func (f inputFlow) directConversation(ctx context.Context) (string, error) {
	persona := f.app.defaultPersona()
	agentInfo := space.PersonaInfo{ID: "assistant", Display: "Sumi"}
	useBuiltinAssistant := isDefaultSumiSource(f.source) || f.usesLegacyDirectAssistant()
	if persona != nil && !useBuiltinAssistant {
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
	if f.app.spaces != nil && f.shouldPersistDirectConversation() {
		var err error
		sp, err = f.app.spaces.Resolve(f.source, agentInfo)
		if err != nil {
			return "", err
		}
		if f.existingUserMessageID != "" {
			excludeMessageID = f.existingUserMessageID
		} else {
			m, err := f.app.spaces.AppendUserMessageWithAttachmentsInThread(sp.ID, command.ParentMessageFrom(ctx), f.input, nil, f.attachments)
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
	var view ContextView
	if sp != nil {
		view = f.seedDirectContext(ctx, s, sp.ID, agentInfo.ID, excludeMessageID)
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
	if err := f.app.autoCompact(ctx, f.source, runtimeName, s, view, f.input, f.attachments); err != nil {
		return "", err
	}
	baseline := len(s.Messages)
	runtimePersona := persona
	if useBuiltinAssistant {
		runtimePersona = nil
	}
	rt, visionLabel, err := f.app.newRuntimeForTurn(runtimeName, runtimePersona, f.attachments)
	if err != nil {
		return "", err
	}
	f.notifyVisionRoute(visionLabel)
	if sp != nil {
		if err := f.app.runTurnAsWithSpaceHistoryNamed(ctx, rt, runtimeName, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	} else {
		if err := f.app.runTurnAsNamed(ctx, rt, runtimeName, f.source, f.personaID, f.input, f.attachments, s); err != nil {
			return "", err
		}
	}
	if visionLabel != "" {
		agent.StripVisionedImageAttachments(s)
	}
	content, reasoning := msg.AssistantOutput(s.Messages[baseline:])
	if sp != nil && (strings.TrimSpace(content) != "" || strings.TrimSpace(reasoning) != "") {
		draft := DraftAssistantMessage{
			AgentID:   agentInfo.ID,
			Content:   content,
			Reasoning: reasoning,
			Added:     s.Messages[baseline:],
		}.Message()
		_, _, err := f.app.spaces.AppendMessageWithRouting(sp.ID, draft, nil, nil)
		if err != nil {
			return "", err
		}
	}
	return content, nil
}

func (f inputFlow) shouldPersistDirectConversation() bool {
	return strings.TrimSpace(f.source) != "desktop"
}

func isDefaultSumiSource(source string) bool {
	source = strings.TrimSpace(source)
	switch source {
	case "cli", "desktop":
		return true
	default:
		return strings.HasPrefix(source, "cli:direct:")
	}
}

func (f inputFlow) usesLegacyDirectAssistant() bool {
	if f.app == nil || f.app.spaces == nil {
		return false
	}
	target := space.MapSource(f.source)
	if target.Kind != space.KindDirectChat || strings.TrimSpace(target.Seed) == "" {
		return false
	}
	sp, err := f.app.spaces.LoadSpace(target.Seed)
	if err != nil || sp == nil {
		return false
	}
	return sp.Kind == space.KindDirectChat &&
		strings.EqualFold(strings.TrimSpace(sp.Key), "Sumi") &&
		space.AgentParticipantID(sp) == ""
}

// seedDirectContext projects the Space into the session and returns the
// ContextView used, so the caller can hand the same projection to autoCompact
// (which needs the full Space message set and identity to anchor a checkpoint).
// The zero ContextView is returned when there is no Space to project from.
func (f inputFlow) seedDirectContext(ctx context.Context, s *session.Session, spaceID, agentID, excludeMessageID string) ContextView {
	if strings.TrimSpace(spaceID) == "" || strings.TrimSpace(agentID) == "" {
		return ContextView{}
	}
	view := f.app.BuildContextView(ContextViewInput{
		SpaceID:          spaceID,
		Source:           f.source,
		ParentMessageID:  command.ParentMessageFrom(ctx),
		AgentID:          agentID,
		ExcludeMessageID: excludeMessageID,
	})
	view.Apply(s)
	return view
}

func (f inputFlow) notifyVisionRoute(label string) {
	label = strings.TrimSpace(label)
	if label == "" || f.app == nil || f.app.bus == nil {
		return
	}
	f.app.bus.Publish(bus.Event{
		Type:   bus.ServiceNotice,
		Source: f.source,
		Text:   "image attachment detected; routed to vision_model: " + label,
	})
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
	if strings.TrimSpace(source) != "" {
		out = append(out, command.MemoryScope{Kind: "channel", Key: strings.TrimSpace(source)})
	}
	if d := strings.TrimSpace(delivery); d != "" && d != strings.TrimSpace(source) {
		out = append(out, command.MemoryScope{Kind: "channel", Key: d})
	}
	if strings.TrimSpace(f.personaID) != "" {
		out = append(out, command.MemoryScope{Kind: "persona", Key: strings.TrimSpace(f.personaID)})
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
	if unknown, ok := f.unknownSlashCommand(); ok {
		f.publishCommandHandled(unknown, nil)
		return unknown, true, nil
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

func (f inputFlow) unknownSlashCommand() (string, bool) {
	input := strings.TrimSpace(f.input)
	if !strings.HasPrefix(input, "/") || strings.HasPrefix(input, "//") {
		return "", false
	}
	name := strings.Fields(strings.TrimPrefix(input, "/"))
	if len(name) == 0 {
		return "", false
	}
	available := f.availableCommandNames()
	return fmt.Sprintf("unknown command: /%s\nSupported inline commands: %s\nUse /help for details.", name[0], strings.Join(available, ", ")), true
}

func (f inputFlow) availableCommandNames() []string {
	cmds := f.app.cmds.All()
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, "/"+c.Name())
	}
	return out
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
