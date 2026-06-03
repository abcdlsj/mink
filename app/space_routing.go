package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

func (a *App) channelRouter() *space.Router {
	if a == nil || a.spaces == nil {
		return nil
	}
	a.spaceRouterOnce.Do(func() {
		a.spaceRouter = space.NewRouter(a.spaces, func(id string) (space.PersonaInfo, bool) {
			id = strings.TrimSpace(id)
			if id == "" || a.personas == nil {
				return space.PersonaInfo{}, false
			}
			if p := a.personas.Get(id); p != nil {
				return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}, true
			}
			lower := strings.ToLower(id)
			for _, p := range a.personas.List() {
				if strings.ToLower(p.Display) == lower || strings.ToLower(p.ID) == lower {
					return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}, true
				}
			}
			return space.PersonaInfo{}, false
		}, 4)
	})
	return a.spaceRouter
}

func sourceUsesRouter(source string) bool {
	return space.SourceUsesRouter(source)
}

type channelInterceptResult struct {
	spaceID string
	wakes   []space.RoutingTarget
	notices []space.RoutingNotice
}

func (a *App) interceptRoutedInput(ctx context.Context, source, content string) (*channelInterceptResult, error) {
	r := a.channelRouter()
	if r == nil {
		return nil, nil
	}
	target := space.MapSource(source)
	if target.Kind != space.KindChannel && target.Kind != space.KindDirectChat {
		return nil, nil
	}
	sp, err := a.spaces.Resolve(source, space.PersonaInfo{})
	if err != nil {
		return nil, err
	}
	parentMessageID := command.ParentMessageFrom(ctx)
	wakes, notices, err := r.RouteUserChannelMessage(sp.ID, content, parentMessageID)
	if err != nil {
		return nil, err
	}
	a.publishRoutingNotices(source, notices)
	result := &channelInterceptResult{
		spaceID: sp.ID,
		wakes:   wakes,
		notices: notices,
	}
	for _, w := range wakes {
		extraNotices := a.runChannelWake(ctx, source, sp.ID, w, content)
		a.publishRoutingNotices(source, extraNotices)
		result.notices = append(result.notices, extraNotices...)
	}
	return result, nil
}

func (a *App) runChannelWake(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string) []space.RoutingNotice {
	persona := a.personas.Get(target.AgentID)
	if persona == nil {
		return nil
	}
	scratch := session.New("scratch:wake:" + uuid.NewString()[:8])
	parentMessageID := ""
	if target.Chain != nil {
		parentMessageID = target.Chain.ParentMessageID
	}
	a.seedWakeContext(scratch, spaceID, parentMessageID, target.AgentID)
	if len(scratch.Messages) == 0 {
		scratch.Add(msg.Message{Role: "user", Content: originUserContent})
	}
	baseline := len(scratch.Messages)
	rt, err := a.newRuntimeFor(persona.Runtime, persona)
	if err != nil {
		return nil
	}
	turn := &agent.Turn{
		Source:          scratch.Source,
		Input:           originUserContent,
		Session:         scratch,
		Bus:             a.bus,
		SpaceID:         spaceID,
		ParentMessageID: parentMessageID,
		AgentID:         persona.ID,
		StreamID:        newStreamID(),
	}
	a.bus.Publish(bus.Event{
		Type:            bus.TurnStarted,
		Source:          scratch.Source,
		SessionID:       scratch.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	})
	runErr := rt.Run(ctx, turn)
	if runErr != nil {
		a.bus.Publish(bus.Event{
			Type:            bus.TurnError,
			Source:          scratch.Source,
			SessionID:       scratch.ID,
			Err:             runErr.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		})
	} else {
		a.bus.Publish(bus.Event{
			Type:            bus.TurnFinished,
			Source:          scratch.Source,
			SessionID:       scratch.ID,
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		})
	}
	content, reasoning := assembleAssistantOutput(scratch.Messages[baseline:])
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return nil
	}
	r := a.channelRouter()
	if r == nil {
		return nil
	}
	resolved := space.ParseMentions(content, r.ResolverFunc(), r.MaxMentions())
	resolved = filterOut(resolved, target.AgentID)

	draft := space.Message{
		AuthorID:        persona.ID,
		AuthorKind:      space.ParticipantAgent,
		Content:         content,
		Reasoning:       reasoning,
		Mentions:        resolved,
		AutoReplyReason: target.Reason,
		ParentMessageID: parentMessageID,
	}
	written, _, err := a.spaces.AppendMessageWithRouting(spaceID, draft, resolved, func(id string) space.PersonaInfo {
		if p := a.personas.Get(id); p != nil {
			return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
		}
		return space.PersonaInfo{ID: id}
	})
	if err != nil {
		return nil
	}
	if target.Chain == nil {
		return nil
	}
	chained, notices, err := r.RouteAgentReply(spaceID, target.Chain.RootMessageID, written.ID, content, target.AgentID)
	if err != nil {
		return notices
	}
	for _, w := range chained {
		extra := a.runChannelWake(ctx, originSource, spaceID, w, content)
		notices = append(notices, extra...)
	}
	return notices
}

func (a *App) publishRoutingNotices(source string, notices []space.RoutingNotice) {
	if a == nil || a.bus == nil || len(notices) == 0 {
		return
	}
	for _, n := range notices {
		a.bus.Publish(bus.Event{
			Type:       string(n.Kind),
			Source:     source,
			SessionID:  n.SpaceID,
			ToolCallID: n.MessageID,
			Tool:       n.AgentID,
			Time:       n.At,
		})
	}
}

const wakeContextLimit = 30

func (a *App) seedWakeContext(s *session.Session, spaceID, parentMessageID, agentID string) {
	if a.spaces == nil {
		return
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil {
		return
	}
	msgs := contextMessages(sp, parentMessageID)
	if n := len(msgs) - wakeContextLimit; n > 0 {
		msgs = msgs[n:]
	}
	for _, m := range msgs {
		s.Add(toRuntimeMessage(m, agentID))
	}
}

func contextMessages(sp *space.Space, parentMessageID string) []space.Message {
	parentMessageID = strings.TrimSpace(parentMessageID)
	if parentMessageID == "" {
		out := make([]space.Message, 0, len(sp.Messages))
		for _, m := range sp.Messages {
			if strings.TrimSpace(m.ParentMessageID) == "" {
				out = append(out, m)
			}
		}
		return out
	}
	out := make([]space.Message, 0)
	for _, m := range sp.Messages {
		if m.ID == parentMessageID || m.ParentMessageID == parentMessageID {
			out = append(out, m)
		}
	}
	return out
}

func toRuntimeMessage(m space.Message, selfID string) msg.Message {
	role := "user"
	if m.AuthorKind == space.ParticipantAgent {
		if m.AuthorID == selfID {
			role = "assistant"
		} else {
			role = "user"
		}
	}
	content := m.Content
	if m.AuthorKind == space.ParticipantAgent && m.AuthorID != selfID && content != "" {
		content = "[" + m.AuthorID + "] " + content
	}
	if m.AuthorKind == space.ParticipantUser && content != "" {
		content = "[user] " + content
	}
	return msg.Message{Role: role, Content: content, AgentID: m.AuthorID}
}

func filterOut(ids []string, drop string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != drop {
			out = append(out, id)
		}
	}
	return out
}

func assembleAssistantOutput(addedMessages []msg.Message) (string, string) {
	var (
		contentParts   []string
		reasoningParts []string
	)
	for _, m := range addedMessages {
		if m.Role != "assistant" {
			continue
		}
		if c := strings.TrimSpace(m.Content); c != "" {
			contentParts = append(contentParts, c)
		}
		if r := strings.TrimSpace(m.Reasoning); r != "" {
			reasoningParts = append(reasoningParts, r)
		}
	}
	return strings.Join(contentParts, "\n"), strings.Join(reasoningParts, "\n")
}
