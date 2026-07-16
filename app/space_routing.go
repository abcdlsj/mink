package app

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		resolver := a.fuzzyPersonaResolver()
		a.spaceRouter = space.NewRouter(a.spaces, resolver.Resolve, 4)
	})
	return a.spaceRouter
}

type channelInterceptResult struct {
	spaceID string
	wakes   []space.RoutingTarget
	notices []space.RoutingNotice
}

func (a *App) interceptRoutedInput(ctx context.Context, source, content string, attachments []msg.Attachment) (*channelInterceptResult, error) {
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
	wakes, notices, err := r.RouteUserChannelMessage(sp.ID, content, parentMessageID, attachments)
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
		extraNotices := a.enqueueChannelWake(source, sp.ID, w, content, attachments)
		a.publishRoutingNotices(source, extraNotices)
		result.notices = append(result.notices, extraNotices...)
	}
	return result, nil
}

func (a *App) enqueueChannelWake(originSource, spaceID string, target space.RoutingTarget, originUserContent string, originAttachments []msg.Attachment) []space.RoutingNotice {
	return a.channelWakePipeline().enqueueChannelWake(originSource, spaceID, target, originUserContent, originAttachments)
}

func (a *App) channelWakeQueue(key string) chan channelWakeJob {
	return a.channelWakePipeline().channelWakeQueue(key)
}

func (a *App) runQueuedChannelWake(job channelWakeJob) {
	a.channelWakePipeline().runQueuedChannelWake(job)
}

func (a *App) RetryChannelAgentReply(ctx context.Context, originSource, spaceID, agentID, parentMessageID, originMessageID, originUserContent string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target := space.RoutingTarget{
		AgentID:         strings.TrimSpace(agentID),
		OriginMessageID: strings.TrimSpace(originMessageID),
		Reason:          "retry",
	}
	if parent := strings.TrimSpace(parentMessageID); parent != "" {
		chain := space.NewRoutingChain(strings.TrimSpace(originMessageID), strings.TrimSpace(spaceID), space.DefaultRoutingBudget)
		chain.ParentMessageID = parent
		target.Chain = chain
	}
	result := a.channelWakePipeline().runChannelWake(ctx, originSource, spaceID, target, originUserContent, nil)
	if len(result.notices) > 0 {
		a.publishRoutingNotices(originSource, result.notices)
	}
	if result.err != nil {
		return "", result.err
	}
	if strings.TrimSpace(result.resultMessageID) == "" {
		return "", nil
	}
	if a == nil || a.spaces == nil {
		return "", nil
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil {
		return "", err
	}
	for _, m := range sp.Messages {
		if m.ID == result.resultMessageID {
			return strings.TrimSpace(m.Content), nil
		}
	}
	return "", nil
}

func (a *App) runChannelWake(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string, originAttachments []msg.Attachment) channelWakeResult {
	return a.channelWakePipeline().runChannelWake(ctx, originSource, spaceID, target, originUserContent, originAttachments)
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

const wakeContextSummaryBudget = 1200

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

func (a *App) syncWakeContext(s *session.Session, source, spaceID, parentMessageID, agentID, excludeMessageID string) ContextView {
	return a.seedWakeContext(s, source, spaceID, parentMessageID, agentID, excludeMessageID)
}

// seedWakeContext rebuilds the wake session from its Space projection and returns
// the ContextView it applied, so the channel/thread wake path can hand the same
// view (and the pending origin input) to autoCompact for an overflow preflight
// before runtime.Run — the collaboration path shares the frozen (Space, Thread,
// Persona) identity, so hard overflow must be guarded here too, not only on the
// Direct/CLI path.
func (a *App) seedWakeContext(s *session.Session, source, spaceID, parentMessageID, agentID, excludeMessageID string) ContextView {
	view := a.BuildContextView(ContextViewInput{
		SpaceID:          spaceID,
		Source:           source,
		ParentMessageID:  parentMessageID,
		AgentID:          agentID,
		ExcludeMessageID: excludeMessageID,
	})
	view.Apply(s)
	return view
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
	// Preserve the Space message ID so a compact boundary can be matched back
	// to the exact Space message on later projection rounds.
	return msg.Message{ID: m.ID, Role: role, Content: content, AgentID: m.AuthorID}
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
