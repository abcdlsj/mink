package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/delivery"
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

// enqueueChannelWake turns a routing target into a durable Delivery and wakes
// the worker to execute it. The wake intent was already persisted on the origin
// message inside RouteUserChannelMessage's atomic append, so this only
// materializes the recoverable execution attempt (create-if-absent, keyed by
// the stable idempotency key) — a crash before or after this call is recovered
// by reconcileDeliveries reading the same persisted intent. If the durable
// store is unavailable (older embedding), it falls back to the legacy volatile
// goroutine queue so behaviour degrades rather than dropping the wake.
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
	if a.store != nil && a.worker != nil {
		d := &delivery.Delivery{
			Kind:            channelWakeKind(parentMessageID),
			SpaceID:         strings.TrimSpace(spaceID),
			ParentMessageID: strings.TrimSpace(parentMessageID),
			OriginMessageID: strings.TrimSpace(target.OriginMessageID),
			AgentID:         strings.TrimSpace(target.AgentID),
		}
		if _, _, err := a.store.Deliveries().CreateIfAbsent(d, time.Now()); err == nil {
			a.worker.wake()
			return nil
		}
	}
	// Legacy fallback: volatile per-lane goroutine queue.
	p.channelWakeQueue(wakeQueueKey(spaceID, parentMessageID, target.AgentID)) <- channelWakeJob{
		originSource:      originSource,
		spaceID:           spaceID,
		target:            target,
		originUserContent: originUserContent,
		originAttachments: append([]msg.Attachment(nil), originAttachments...),
	}
	return nil
}

// channelWakeKind selects the durable delivery kind for a first-round wake: a
// parent message means the wake lives in a thread.
func channelWakeKind(parentMessageID string) delivery.Kind {
	if strings.TrimSpace(parentMessageID) != "" {
		return delivery.KindThreadWake
	}
	return delivery.KindChannelWake
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

// runChannelWakeBound is the durable-worker entrypoint: it runs one claimed
// Delivery's turn and finalizes the reply INTO the pre-created assistant
// placeholder (binding.resultMessageID) rather than appending a new message.
// Turn events carry the DeliveryID and the stable placeholder MessageID so the
// desktop backend binds instead of appending, and never deletes the message on
// TurnFinished. It is the exact same turn machinery as runChannelWake — only the
// finalize + chained-wake continuation differ (persist intents + create downstream
// deliveries instead of enqueuing volatile jobs).
func (p *channelWakePipeline) runChannelWakeBound(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string, originAttachments []msg.Attachment, binding *wakeBinding) channelWakeResult {
	return p.runChannelWakeImpl(ctx, originSource, spaceID, target, originUserContent, originAttachments, binding)
}

func (p *channelWakePipeline) runChannelWake(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string, originAttachments []msg.Attachment) channelWakeResult {
	return p.runChannelWakeImpl(ctx, originSource, spaceID, target, originUserContent, originAttachments, nil)
}

// runChannelWakeImpl is the shared turn executor. binding == nil is the
// synchronous/direct append path (unchanged legacy behavior, keeps the sync
// tests green); binding != nil is the durable worker path that finalizes into a
// placeholder under a delivery fence.
func (p *channelWakePipeline) runChannelWakeImpl(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string, originAttachments []msg.Attachment, binding *wakeBinding) channelWakeResult {
	a := p.app
	persona := a.personas.Get(target.AgentID)
	if persona == nil {
		return channelWakeResult{emptyOutput: true}
	}
	parentMessageID := ""
	if target.Chain != nil {
		parentMessageID = target.Chain.ParentMessageID
	}
	// When bound to a delivery, every turn event carries the DeliveryID and the
	// stable placeholder MessageID so the desktop backend binds to (never appends
	// or deletes) that one message.
	deliveryID := ""
	boundMessageID := ""
	if binding != nil {
		deliveryID = binding.deliveryID
		boundMessageID = binding.resultMessageID
	}
	stamp := func(ev bus.Event) bus.Event {
		ev.DeliveryID = deliveryID
		if boundMessageID != "" {
			ev.MessageID = boundMessageID
		}
		return ev
	}
	sessionSource := wakeSessionSource(originSource, parentMessageID, target.AgentID)
	s, err := a.sessions.Current(sessionSource)
	if err != nil {
		return channelWakeResult{err: err}
	}
	view := a.syncWakeContext(s, originSource, spaceID, parentMessageID, target.AgentID, target.OriginMessageID)

	// Persona runtime may be empty; runtimeFactory falls back to cfg.Runtime, so
	// the actual consumer of this turn is effectiveRuntimeName, NOT the raw
	// persona.Runtime. Resolve it once and use the same name for the overflow
	// preflight budget, the runtime build, and the external-vs-native memory
	// branch — otherwise an empty persona runtime under a global external
	// cfg.Runtime gets budgeted as native (the consumer/summarizer window mixing
	// we forbid).
	runtimeName := a.effectiveRuntimeName(persona.Runtime)

	// Open the turn's stream and emit TurnStarted BEFORE the overflow preflight,
	// so the whole turn follows one normal lifecycle: TurnStarted -> preflight ->
	// (TurnError | run). The Desktop backend lands a pending agent message on
	// TurnStarted and persists it as status=failed when a TurnError arrives on the
	// SAME stream, so a pre-run failure (hard overflow, runtime build) is both
	// live-visible and durable after reload — no stream-less side-band error and
	// no direct failed-message Append. streamID is reused as the turn's StreamID
	// below so chunks / TurnFinished stay on the same stream.
	streamID := newStreamID()
	a.bus.Publish(stamp(bus.Event{
		Type:            bus.TurnStarted,
		Source:          s.Source,
		SessionID:       s.ID,
		SpaceID:         spaceID,
		ParentMessageID: parentMessageID,
		AgentID:         persona.ID,
		StreamID:        streamID,
	}))
	failStream := func(err error) channelWakeResult {
		a.bus.Publish(stamp(bus.Event{
			Type:            bus.TurnError,
			Source:          s.Source,
			SessionID:       s.ID,
			Err:             err.Error(),
			SpaceID:         spaceID,
			ParentMessageID: parentMessageID,
			AgentID:         persona.ID,
			StreamID:        streamID,
		}))
		return channelWakeResult{err: err}
	}

	// Channel/thread wake always injects history into the prompt (IncludeHistory
	// + DisableExternalResume below), so run the same overflow preflight the
	// Direct/CLI path runs, against this view and the pending origin input,
	// before the runtime builds the turn.
	if err := a.autoCompact(ctx, originSource, runtimeName, s, view, originUserContent, originAttachments); err != nil {
		return failStream(err)
	}
	collaborationBrief := a.routedCollaborationBrief(spaceID, parentMessageID, target)
	baseline := len(s.Messages)
	rt, visionLabel, err := a.newRuntimeForTurn(runtimeName, persona, originAttachments)
	if err != nil {
		return failStream(err)
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
		StreamID:              streamID,
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
	a.prepareMemoryForTurn(ctx, turn, externalRuntimeName(runtimeName))
	// TurnStarted was already emitted on streamID before the preflight; the turn
	// reuses that same stream, so chunks and TurnFinished/TurnError below stay on
	// one continuous lifecycle.
	runErr := rt.Run(ctx, turn)
	if runErr == nil && externalRuntimeName(runtimeName) {
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
		a.bus.Publish(stamp(bus.Event{
			Type:            bus.TurnError,
			Source:          s.Source,
			SessionID:       s.ID,
			Err:             runErr.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		}))
		return channelWakeResult{err: runErr}
	}
	if err := a.sessions.Save(s); err != nil {
		_ = a.persistChannelWakeFailure(spaceID, parentMessageID, target.AgentID, err)
		a.bus.Publish(stamp(bus.Event{
			Type:            bus.TurnError,
			Source:          s.Source,
			SessionID:       s.ID,
			Err:             err.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		}))
		return channelWakeResult{err: err}
	}
	a.bus.Publish(stamp(bus.Event{
		Type:            bus.TurnFinished,
		Source:          s.Source,
		SessionID:       s.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	}))
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
	if binding != nil {
		// Durable worker path: a stale fence means a newer owner reclaimed this
		// lane; do not write the reply over them.
		if binding.guard.stale() {
			return channelWakeResult{}
		}
		return p.finalizeBoundReply(spaceID, parentMessageID, target, binding, content, reasoning, resolved, added, attachments)
	}
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

// finalizeBoundReply is the durable worker's "reply-before-complete" step. It
// fills the pre-created placeholder (binding.resultMessageID) with the produced
// reply and, in the SAME Space commit, persists any continuation wake intents the
// reply triggers (FinalizeDeliveryMessage.buildIntents). Only after that commit
// does it materialize the downstream deliveries from the persisted intents, so a
// crash between reply and downstream-create is recoverable by reconcile reading
// those intents. It never appends or deletes — the one-placeholder-per-DeliveryID
// invariant holds.
func (p *channelWakePipeline) finalizeBoundReply(spaceID, parentMessageID string, target space.RoutingTarget, binding *wakeBinding, content, reasoning string, resolved []string, added []msg.Message, attachments []msg.Attachment) channelWakeResult {
	a := p.app
	chainRoot := ""
	if target.Chain != nil {
		chainRoot = strings.TrimSpace(target.Chain.RootMessageID)
	}
	replyReason := a.routedReplyReason(spaceID, target)
	// buildIntents is pure: it may only read the assigned reply id and the
	// immutable pre-finalize snapshot. It caps continuation intents by the durable
	// routing budget = DefaultRoutingBudget - (intents already recorded for this
	// chain root), so total wakes per chain never exceed the budget across crashes.
	buildIntents := func(assignedID string, existing []space.Message) []space.RoutingIntent {
		if chainRoot == "" || len(resolved) == 0 {
			return nil
		}
		spent := space.CountChainIntents(existing, chainRoot)
		remaining := space.DefaultRoutingBudget - spent
		if remaining <= 0 {
			return nil
		}
		intents := make([]space.RoutingIntent, 0, len(resolved))
		for _, id := range resolved {
			if len(intents) >= remaining {
				break
			}
			intents = append(intents, space.RoutingIntent{
				AgentID:         id,
				Kind:            "chain",
				Reason:          replyReason,
				ParentMessageID: parentMessageID,
				ChainRoot:       chainRoot,
			})
		}
		return intents
	}
	personas := a.fuzzyPersonaResolver()
	// The live-lease authority check inside the store must use the same clock the
	// lease/renew path uses (a fake clock in tests). binding.now carries the
	// worker's clock; nil defaults to wall time for non-worker callers.
	now := time.Now()
	if binding.now != nil {
		now = binding.now()
	}
	written, _, err := a.spaces.FinalizeDeliveryMessage(spaceID, binding.resultMessageID, binding.deliveryID, binding.fence.OwnerID, binding.fence.Version, now, func(m *space.Message) {
		m.Content = content
		m.Reasoning = reasoning
		m.Mentions = resolved
		m.AutoReplyReason = strings.TrimSpace(replyReason)
		m.Status = ""
		m.Error = ""
		m.Usage = msg.AssistantUsage(added)
		m.RuntimeMeta = msg.AssistantRuntimeMeta(added)
		m.Attachments = attachments
	}, resolved, personas.Info, buildIntents)
	if err != nil {
		return channelWakeResult{err: err}
	}
	result := channelWakeResult{
		resultMessageID: binding.resultMessageID,
		outcome:         shortOutcomeForTask(content),
	}
	// Materialize downstream deliveries from the just-persisted continuation
	// intents. CreateIfAbsent is idempotent by StableKey, so a reconcile that
	// races this create resolves to the same Delivery.
	notices := a.createContinuationDeliveries(spaceID, written)
	result.notices = append(result.notices, notices...)
	// Any mention that did not fit the budget is reported exhausted, mirroring the
	// legacy fanOut notice.
	result.notices = append(result.notices, budgetDropNotices(spaceID, written, resolved)...)
	return result
}

// budgetDropNotices emits a budget-exhausted notice for each resolved mention
// that did not receive a persisted continuation intent (the reply named more
// agents than the remaining chain budget allowed).
func budgetDropNotices(spaceID string, reply space.Message, resolved []string) []space.RoutingNotice {
	kept := make(map[string]struct{}, len(reply.RoutingIntents))
	for _, it := range reply.RoutingIntents {
		kept[it.AgentID] = struct{}{}
	}
	var notices []space.RoutingNotice
	for _, id := range resolved {
		if _, ok := kept[id]; ok {
			continue
		}
		notices = append(notices, space.RoutingNotice{
			Kind:      space.NoticeBudgetExhausted,
			SpaceID:   spaceID,
			MessageID: reply.ID,
			AgentID:   id,
			At:        time.Now(),
		})
	}
	return notices
}

func wakeQueueKey(spaceID, parentMessageID, agentID string) string {
	return spaceID + "\x00" + parentMessageID + "\x00" + agentID
}

func (a *App) persistChannelWakeFailure(spaceID, parentMessageID, agentID string, err error) error {
	// The pending agent message is the user-visible failure surface.
	// Adding a separate system message makes retry look like a new participant reply.
	return nil
}
