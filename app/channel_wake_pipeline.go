package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

type channelWakePipeline struct {
	app *App
}

type channelWakeJob struct {
	originSource      string
	spaceID           string
	target            space.RoutingTarget
	originUserContent string
	originAttachments []msg.Attachment
}

type channelWakeResult struct {
	notices         []space.RoutingNotice
	resultMessageID string
	outcome         string
	err             error
	emptyOutput     bool
}

func (a *App) channelWakePipeline() *channelWakePipeline {
	if a == nil {
		return nil
	}
	return &channelWakePipeline{app: a}
}

func (p *channelWakePipeline) enqueueChannelWake(originSource, spaceID string, target space.RoutingTarget, originUserContent string, originAttachments []msg.Attachment) []space.RoutingNotice {
	a := p.app
	if a == nil {
		return nil
	}
	parentMessageID := ""
	if target.Chain != nil {
		parentMessageID = target.Chain.ParentMessageID
	}
	a.bus.Publish(bus.Event{
		Type:            bus.TurnQueued,
		Source:          originSource,
		SessionID:       spaceID,
		SpaceID:         spaceID,
		ParentMessageID: parentMessageID,
		AgentID:         target.AgentID,
	})
	p.channelWakeQueue(wakeQueueKey(spaceID, parentMessageID, target.AgentID)) <- channelWakeJob{
		originSource:      originSource,
		spaceID:           spaceID,
		target:            target,
		originUserContent: originUserContent,
		originAttachments: append([]msg.Attachment(nil), originAttachments...),
	}
	return nil
}

func (p *channelWakePipeline) channelWakeQueue(key string) chan channelWakeJob {
	a := p.app
	a.wakeMu.Lock()
	defer a.wakeMu.Unlock()
	if a.wakeQueues == nil {
		a.wakeQueues = map[string]chan channelWakeJob{}
	}
	if q, ok := a.wakeQueues[key]; ok {
		return q
	}
	q := make(chan channelWakeJob, 128)
	a.wakeQueues[key] = q
	go func() {
		for job := range q {
			p.runQueuedChannelWake(job)
		}
	}()
	return q
}

func (p *channelWakePipeline) runQueuedChannelWake(job channelWakeJob) {
	result := p.runChannelWake(context.Background(), job.originSource, job.spaceID, job.target, job.originUserContent, job.originAttachments)
	if len(result.notices) > 0 && p.app != nil {
		p.app.publishRoutingNotices(job.originSource, result.notices)
	}
}

func (p *channelWakePipeline) runChannelWake(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string, originAttachments []msg.Attachment) channelWakeResult {
	a := p.app
	persona := a.personas.Get(target.AgentID)
	if persona == nil {
		return channelWakeResult{emptyOutput: true}
	}
	parentMessageID := ""
	if target.Chain != nil {
		parentMessageID = target.Chain.ParentMessageID
	}
	sessionSource := wakeSessionSource(originSource, parentMessageID, target.AgentID)
	s, err := a.sessions.Current(sessionSource)
	if err != nil {
		return channelWakeResult{err: err}
	}
	a.syncWakeContext(s, originSource, spaceID, parentMessageID, target.AgentID, target.OriginMessageID)
	collaborationBrief := a.routedCollaborationBrief(spaceID, parentMessageID, target)
	baseline := len(s.Messages)
	rt, visionLabel, err := a.newRuntimeForTurn(persona.Runtime, persona, originAttachments)
	if err != nil {
		return channelWakeResult{err: err}
	}
	if visionLabel != "" {
		a.bus.Publish(bus.Event{
			Type:    bus.ServiceNotice,
			Source:  s.Source,
			SpaceID: spaceID,
			AgentID: persona.ID,
			Text:    "image attachment detected; routed to vision_model: " + visionLabel,
		})
	}
	turn := &agent.Turn{
		Source:                s.Source,
		Input:                 originUserContent,
		Attachments:           originAttachments,
		Session:               s,
		Bus:                   a.bus,
		SpaceID:               spaceID,
		ParentMessageID:       parentMessageID,
		AgentID:               persona.ID,
		StreamID:              newStreamID(),
		CollaborationBrief:    collaborationBrief,
		IncludeHistory:        true,
		DisableExternalResume: true,
		BlockedTools:          mergeToolBlocks(taskToolBlocks(persona), memoryToolBlocks(persona)),
	}
	ctx = command.WithSource(ctx, originSource)
	ctx = command.WithPersona(ctx, persona.ID)
	if parentMessageID != "" {
		ctx = command.WithParentMessage(ctx, parentMessageID)
	}
	ctx = command.WithRunContext(ctx, inputFlow{
		app:       a,
		source:    originSource,
		personaID: persona.ID,
		input:     originUserContent,
	}.runContextWithSession(ctx, sessionSource))
	a.prepareMemoryForTurn(ctx, turn, externalRuntimeName(persona.Runtime))
	a.bus.Publish(bus.Event{
		Type:            bus.TurnStarted,
		Source:          s.Source,
		SessionID:       s.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	})
	runErr := rt.Run(ctx, turn)
	if runErr == nil && externalRuntimeName(persona.Runtime) {
		a.processAssistantMemoryInSession(ctx, turn, s, baseline)
	}
	if runErr == nil && visionLabel != "" {
		agent.StripVisionedImageAttachments(s)
	}
	if runErr != nil {
		saveErr := a.sessions.Save(s)
		if saveErr != nil {
			runErr = fmt.Errorf("%w; save session: %v", runErr, saveErr)
		}
		_ = a.persistChannelWakeFailure(spaceID, parentMessageID, target.AgentID, runErr)
		a.bus.Publish(bus.Event{
			Type:            bus.TurnError,
			Source:          s.Source,
			SessionID:       s.ID,
			Err:             runErr.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		})
		return channelWakeResult{err: runErr}
	}
	if err := a.sessions.Save(s); err != nil {
		_ = a.persistChannelWakeFailure(spaceID, parentMessageID, target.AgentID, err)
		a.bus.Publish(bus.Event{
			Type:            bus.TurnError,
			Source:          s.Source,
			SessionID:       s.ID,
			Err:             err.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		})
		return channelWakeResult{err: err}
	}
	a.bus.Publish(bus.Event{
		Type:            bus.TurnFinished,
		Source:          s.Source,
		SessionID:       s.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	})
	content, reasoning := msg.AssistantOutput(s.Messages[baseline:])
	attachments := assistantAttachments(s.Messages[baseline:], "memory_commit")
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return channelWakeResult{emptyOutput: true}
	}
	r := a.channelRouter()
	if r == nil {
		return channelWakeResult{emptyOutput: true}
	}
	resolved := space.ParseMentions(content, r.ResolverFunc(), r.MaxMentions())
	resolved = filterOut(resolved, target.AgentID)

	added := s.Messages[baseline:]
	draft := DraftAssistantMessage{
		AgentID:         persona.ID,
		Content:         content,
		Reasoning:       reasoning,
		Mentions:        resolved,
		AutoReplyReason: a.routedReplyReason(spaceID, target),
		ParentMessageID: parentMessageID,
		Added:           added,
		Attachments:     attachments,
	}.Message()
	personas := a.fuzzyPersonaResolver()
	written, _, err := a.spaces.AppendMessageWithRouting(spaceID, draft, resolved, personas.Info)
	if err != nil {
		return channelWakeResult{err: err}
	}
	result := channelWakeResult{
		resultMessageID: written.ID,
		outcome:         shortOutcomeForTask(content),
	}
	if target.Chain == nil {
		return result
	}
	chained, notices, err := r.RouteAgentReply(spaceID, target.Chain.RootMessageID, written.ID, content, target.AgentID)
	if err != nil {
		result.notices = notices
		result.err = err
		return result
	}
	result.notices = append(result.notices, notices...)
	for _, w := range chained {
		extra := p.enqueueChannelWake(originSource, spaceID, w, content, nil)
		result.notices = append(result.notices, extra...)
	}
	return result
}

func wakeQueueKey(spaceID, parentMessageID, agentID string) string {
	return spaceID + "\x00" + parentMessageID + "\x00" + agentID
}

func (a *App) persistChannelWakeFailure(spaceID, parentMessageID, agentID string, err error) error {
	// The pending agent message is the user-visible failure surface.
	// Adding a separate system message makes retry look like a new participant reply.
	return nil
}
