package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
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
		extraNotices := a.enqueueChannelWake(source, sp.ID, w, content)
		a.publishRoutingNotices(source, extraNotices)
		result.notices = append(result.notices, extraNotices...)
	}
	return result, nil
}

type channelWakeJob struct {
	originSource      string
	spaceID           string
	target            space.RoutingTarget
	originUserContent string
	taskID            string
}

type channelWakeResult struct {
	notices         []space.RoutingNotice
	resultMessageID string
	outcome         string
	err             error
	emptyOutput     bool
}

func (a *App) enqueueChannelWake(originSource, spaceID string, target space.RoutingTarget, originUserContent string) []space.RoutingNotice {
	if a == nil {
		return nil
	}
	triggerID := strings.TrimSpace(target.OriginMessageID)
	if triggerID == "" && target.Chain != nil {
		triggerID = target.Chain.RootMessageID
	}
	parentMessageID := ""
	if target.Chain != nil {
		parentMessageID = target.Chain.ParentMessageID
	}
	taskID := ""
	if a.tasks != nil {
		tk, err := a.tasks.Create(taskpkg.CreateTaskInput{
			SpaceID:          spaceID,
			TriggerMessageID: triggerID,
			SourceThreadID:   parentMessageID,
			InitiatorID:      a.spaces.UserParticipant().ID,
			WorkerID:         target.AgentID,
			Title:            wakeTaskTitle(originUserContent),
			Source:           originSource,
		})
		if err == nil && tk != nil {
			taskID = tk.ID
		}
	}
	a.bus.Publish(bus.Event{
		Type:            bus.TurnQueued,
		Source:          originSource,
		SessionID:       spaceID,
		TaskID:          taskID,
		SpaceID:         spaceID,
		ParentMessageID: parentMessageID,
		AgentID:         target.AgentID,
	})
	a.channelWakeQueue(wakeQueueKey(spaceID, parentMessageID, target.AgentID)) <- channelWakeJob{
		originSource:      originSource,
		spaceID:           spaceID,
		target:            target,
		originUserContent: originUserContent,
		taskID:            taskID,
	}
	return nil
}

func (a *App) channelWakeQueue(key string) chan channelWakeJob {
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
			a.runQueuedChannelWake(job)
		}
	}()
	return q
}

func wakeQueueKey(spaceID, parentMessageID, agentID string) string {
	return spaceID + "\x00" + parentMessageID + "\x00" + agentID
}

func wakeTaskTitle(content string) string {
	content = strings.ReplaceAll(strings.TrimSpace(content), "\n", " ")
	rs := []rune(content)
	if len(rs) > 76 {
		content = string(rs[:76]) + "..."
	}
	if content == "" {
		return "Agent wake"
	}
	return content
}

func (a *App) runQueuedChannelWake(job channelWakeJob) {
	var runID string
	if job.taskID != "" && a.tasks != nil {
		if _, err := a.tasks.Update(job.taskID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning}); err == nil {
			if run, err := a.tasks.StartRun(job.taskID); err == nil && run != nil {
				runID = run.ID
			}
		}
	}
	result := a.runChannelWake(context.Background(), job.originSource, job.spaceID, job.target, job.originUserContent)
	if len(result.notices) > 0 {
		a.publishRoutingNotices(job.originSource, result.notices)
	}
	if job.taskID == "" || a.tasks == nil {
		return
	}
	status := taskpkg.StatusFinished
	outcome := result.outcome
	if result.err != nil {
		status = taskpkg.StatusFailed
		outcome = result.err.Error()
	} else if result.emptyOutput {
		status = taskpkg.StatusEmptyOutput
	}
	_, _ = a.tasks.Update(job.taskID, taskpkg.UpdateTaskInput{
		Status:          status,
		Outcome:         outcome,
		ResultMessageID: result.resultMessageID,
	})
	if runID != "" {
		_, _ = a.tasks.FinishRun(runID, taskpkg.FinishRunInput{Status: status})
	}
}

func (a *App) runChannelWake(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string) channelWakeResult {
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
	rt, err := a.newRuntimeFor(persona.Runtime, persona)
	if err != nil {
		return channelWakeResult{err: err}
	}
	turn := &agent.Turn{
		Source:                s.Source,
		Input:                 originUserContent,
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
	}.runContextWithSession(ctx, sessionSource))
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
	} else {
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
	}
	content, reasoning := msg.AssistantOutput(s.Messages[baseline:])
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return channelWakeResult{emptyOutput: true}
	}
	r := a.channelRouter()
	if r == nil {
		return channelWakeResult{emptyOutput: true}
	}
	resolved := space.ParseMentions(content, r.ResolverFunc(), r.MaxMentions())
	resolved = filterOut(resolved, target.AgentID)

	draft := space.Message{
		AuthorID:        persona.ID,
		AuthorKind:      space.ParticipantAgent,
		Content:         content,
		Reasoning:       reasoning,
		Mentions:        resolved,
		AutoReplyReason: a.routedReplyReason(spaceID, target),
		ParentMessageID: parentMessageID,
		Usage:           msg.AssistantUsage(s.Messages[baseline:]),
		RuntimeMeta:     msg.AssistantRuntimeMeta(s.Messages[baseline:]),
	}
	written, _, err := a.spaces.AppendMessageWithRouting(spaceID, draft, resolved, func(id string) space.PersonaInfo {
		if p := a.personas.Get(id); p != nil {
			return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
		}
		return space.PersonaInfo{ID: id}
	})
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
		extra := a.enqueueChannelWake(originSource, spaceID, w, content)
		result.notices = append(result.notices, extra...)
	}
	return result
}

func shortOutcomeForTask(content string) string {
	content = strings.ReplaceAll(strings.TrimSpace(content), "\n", " ")
	rs := []rune(content)
	if len(rs) > 180 {
		return string(rs[:180]) + "..."
	}
	return content
}

func (a *App) publishRoutingNotices(source string, notices []space.RoutingNotice) {
	if a == nil || a.bus == nil || len(notices) == 0 {
		return
	}
	for _, n := range notices {
		parentMessageID := a.routingNoticeParent(n.SpaceID, n.MessageID)
		_ = a.persistRoutingNotice(n, parentMessageID)
		a.bus.Publish(bus.Event{
			Type:            string(n.Kind),
			Source:          source,
			SessionID:       n.SpaceID,
			SpaceID:         n.SpaceID,
			ParentMessageID: parentMessageID,
			ToolCallID:      n.MessageID,
			Tool:            n.AgentID,
			Time:            n.At,
		})
	}
}

func (a *App) persistRoutingNotice(n space.RoutingNotice, parentID string) error {
	if a == nil || a.spaces == nil {
		return nil
	}
	text := routingNoticeText(n.Kind, n.AgentID)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return a.appendSystemSpaceMessage(n.SpaceID, parentID, text)
}

func routingNoticeText(kind space.RoutingNoticeKind, agentID string) string {
	switch kind {
	case space.NoticeChannelNoTarget:
		return "No agent picked this up. Mention an agent or enable listening."
	case space.NoticeListeningAmbiguous:
		return "Mention a specific agent."
	case space.NoticeListeningNoMatch:
		return "No listening agent matched this. Mention one explicitly."
	case space.NoticeBudgetExhausted:
		return routingAgentText(agentID, "routing budget exhausted")
	case space.NoticeDuplicateSkipped:
		return routingAgentText(agentID, "duplicate route skipped")
	case space.NoticeUnknownMentionDrop:
		return routingAgentText(agentID, "unknown mention ignored")
	default:
		return ""
	}
}

func routingAgentText(agentID, suffix string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return suffix + "."
	}
	return "@" + agentID + ": " + suffix + "."
}

func (a *App) routingNoticeParent(spaceID, messageID string) string {
	if a == nil || a.spaces == nil || strings.TrimSpace(messageID) == "" {
		return ""
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ""
	}
	for _, m := range sp.Messages {
		if m.ID == messageID {
			return strings.TrimSpace(m.ParentMessageID)
		}
	}
	return ""
}

func (a *App) persistChannelWakeFailure(spaceID, parentMessageID, agentID string, err error) error {
	if err == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	prefix := "Agent failed"
	if agentID != "" {
		prefix = "@" + agentID + " failed"
	}
	return a.appendSystemSpaceMessage(spaceID, parentMessageID, prefix+": "+err.Error())
}

func (a *App) appendSystemSpaceMessage(spaceID, parentMessageID, content string) error {
	if a == nil || a.spaces == nil || strings.TrimSpace(spaceID) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	at := time.Now()
	_, _, err := a.spaces.AppendMessageWithRouting(spaceID, space.Message{
		AuthorID:        "sumi",
		AuthorKind:      space.ParticipantSystem,
		Content:         strings.TrimSpace(content),
		ParentMessageID: strings.TrimSpace(parentMessageID),
		CreatedAt:       at,
	}, nil, nil)
	return err
}

const (
	wakeContextTokenFallback = 20000
	wakeContextSummaryBudget = 1200
)

func wakeSessionSource(originSource, parentMessageID, agentID string) string {
	originSource = strings.TrimSpace(originSource)
	if originSource == "" {
		originSource = "desktop"
	}
	if p := strings.TrimSpace(parentMessageID); p != "" {
		originSource += ":thread:" + p
	}
	return personaSessionSource(originSource, agentID)
}

func (a *App) routedCollaborationBrief(spaceID, parentMessageID string, target space.RoutingTarget) string {
	if a == nil || a.spaces == nil {
		return ""
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ""
	}
	lines := []string{
		"- scope: " + collaborationScope(parentMessageID),
		"- trigger: " + collaborationTrigger(target),
		"- target agent: " + strings.TrimSpace(target.AgentID),
	}
	if target.OriginMessageID != "" {
		lines = append(lines, "- trigger message: "+collaborationMessagePreview(sp, target.OriginMessageID))
	}
	if target.Chain != nil {
		lines = append(lines, fmt.Sprintf("- chain budget remaining: %d", target.Chain.Budget))
	} else {
		lines = append(lines, "- chain budget remaining: unknown")
	}
	if recent := recentAgentConclusions(sp, parentMessageID, target.AgentID, 3); len(recent) > 0 {
		lines = append(lines, "- recent agent conclusions:")
		for _, line := range recent {
			lines = append(lines, "  - "+line)
		}
	}
	lines = append(lines, "- instruction: answer as part of this shared discussion; add the missing piece or next action.")
	return strings.Join(lines, "\n")
}

func (a *App) routedReplyReason(spaceID string, target space.RoutingTarget) string {
	if reason := strings.TrimSpace(target.Reason); reason != "" {
		return reason
	}
	if target.Chain != nil && target.Chain.RootMessageID != target.OriginMessageID {
		if author := a.routingOriginAgent(spaceID, target.OriginMessageID); author != "" {
			return "called by @" + author
		}
		return "called by another agent"
	}
	return "called by mention"
}

func (a *App) routingOriginAgent(spaceID, messageID string) string {
	if a == nil || a.spaces == nil || strings.TrimSpace(messageID) == "" {
		return ""
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ""
	}
	for _, m := range sp.Messages {
		if m.ID == messageID && m.AuthorKind == space.ParticipantAgent {
			return strings.TrimSpace(m.AuthorID)
		}
	}
	return ""
}

func collaborationScope(parentMessageID string) string {
	if strings.TrimSpace(parentMessageID) != "" {
		return "thread"
	}
	return "channel"
}

func collaborationTrigger(target space.RoutingTarget) string {
	if strings.TrimSpace(target.Reason) != "" {
		return strings.TrimSpace(target.Reason)
	}
	if target.Chain != nil && target.Chain.RootMessageID != target.OriginMessageID {
		return "agent mention"
	}
	return "explicit mention"
}

func collaborationMessagePreview(sp *space.Space, messageID string) string {
	for _, m := range sp.Messages {
		if m.ID == messageID {
			author := strings.TrimSpace(m.AuthorID)
			if author == "" {
				author = string(m.AuthorKind)
			}
			return author + ": " + trimText(strings.TrimSpace(m.Content), 240)
		}
	}
	return strings.TrimSpace(messageID)
}

func recentAgentConclusions(sp *space.Space, parentMessageID, selfID string, limit int) []string {
	if sp == nil || limit <= 0 {
		return nil
	}
	parentMessageID = strings.TrimSpace(parentMessageID)
	selfID = strings.TrimSpace(selfID)
	out := make([]string, 0, limit)
	for i := len(sp.Messages) - 1; i >= 0 && len(out) < limit; i-- {
		m := sp.Messages[i]
		if m.AuthorKind != space.ParticipantAgent || strings.TrimSpace(m.Content) == "" {
			continue
		}
		if strings.TrimSpace(m.AuthorID) == selfID {
			continue
		}
		if strings.TrimSpace(m.ParentMessageID) != parentMessageID {
			continue
		}
		out = append(out, strings.TrimSpace(m.AuthorID)+": "+trimText(strings.TrimSpace(m.Content), 200))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (a *App) syncWakeContext(s *session.Session, source, spaceID, parentMessageID, agentID, excludeMessageID string) {
	a.seedWakeContext(s, source, spaceID, parentMessageID, agentID, excludeMessageID, a.wakeContextTokenLimit())
}

func (a *App) wakeContextTokenLimit() int {
	if limit := a.compactTokenLimit(); limit > 0 {
		return limit
	}
	return wakeContextTokenFallback
}

func (a *App) seedWakeContext(s *session.Session, source, spaceID, parentMessageID, agentID, excludeMessageID string, tokenLimit int) {
	a.BuildContextView(ContextViewInput{
		SpaceID:          spaceID,
		Source:           source,
		ParentMessageID:  parentMessageID,
		AgentID:          agentID,
		ExcludeMessageID: excludeMessageID,
		TokenLimit:       tokenLimit,
	}).Apply(s)
}

func filterContextMessages(msgs []space.Message, excludeMessageID string, profile ContextProfile) []space.Message {
	excludeMessageID = strings.TrimSpace(excludeMessageID)
	out := make([]space.Message, 0, len(msgs))
	for _, m := range msgs {
		if excludeMessageID != "" && m.ID == excludeMessageID {
			continue
		}
		if contextRejectReason(m, profile) != "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func eligibleContextMessage(m space.Message, profile ContextProfile) bool {
	return contextRejectReason(m, profile) == ""
}

func contextRejectReason(m space.Message, profile ContextProfile) string {
	content := strings.TrimSpace(m.Content)
	if content == "" && strings.TrimSpace(m.Reasoning) == "" {
		return "empty"
	}
	if m.AuthorKind == space.ParticipantSystem {
		return "system"
	}
	if content == "NO_REPLY" || strings.HasPrefix(content, "NO_REPLY ") {
		return "no_reply"
	}
	if noisyRuntimeContent(content) {
		return "runtime_noise"
	}
	return ""
}

func noisyRuntimeContent(content string) bool {
	c := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	if c == "" {
		return false
	}
	prefixes := []string{
		"send failed:",
		"agent failed:",
		"no agent picked this up",
		"no listening agent matched",
		"mention a specific agent",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(c, p) {
			return true
		}
	}
	contains := []string{
		" failed: ",
		"unsupported image type",
		"unsupported telegram image type",
		"unknown variant `image_url`",
		"unknown variant image_url",
		"expected `text`",
		"expected text",
		"image failed to send",
		"failed to deserialize the json body",
		"http 400",
	}
	for _, needle := range contains {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

func boundedContextMessages(msgs []space.Message, agentID string, tokenLimit int) []space.Message {
	if tokenLimit <= 0 {
		tokenLimit = wakeContextTokenFallback
	}
	out := make([]space.Message, 0, len(msgs))
	total := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		rm := toRuntimeMessage(m, agentID)
		cost := estimateMessage(rm)
		if cost > tokenLimit && len(out) == 0 {
			out = append(out, m)
			break
		}
		if total+cost > tokenLimit {
			break
		}
		total += cost
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func wakeContextSummary(msgs []space.Message, agentID string, provenance summaryProvenance) string {
	runtimeMsgs := make([]msg.Message, 0, len(msgs))
	var start, end time.Time
	for _, m := range msgs {
		runtimeMsgs = append(runtimeMsgs, toRuntimeMessage(m, agentID))
		if !m.CreatedAt.IsZero() {
			if start.IsZero() || m.CreatedAt.Before(start) {
				start = m.CreatedAt
			}
			if end.IsZero() || m.CreatedAt.After(end) {
				end = m.CreatedAt
			}
		}
	}
	if len(runtimeMsgs) == 0 {
		return ""
	}
	summary := heuristicSummary(runtimeMsgs)
	summary = summaryWithProvenance(summary, provenance, start, end)
	if estimateMessage(msg.Message{Role: "system", Content: summary}) <= wakeContextSummaryBudget {
		return summary
	}
	return trimText(summary, wakeContextSummaryBudget*4)
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
