package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

const desktopSource = "desktop"

const defaultChannelID = "desktop:default"
const defaultSumiDirectTitle = "Sumi"
const pendingRecoveryGrace = 2 * time.Minute

const (
	pendingMetaStreamID  = "pending_stream_id"
	pendingMetaSessionID = "pending_session_id"
	pendingMetaSource    = "pending_source"
	pendingMetaStartedAt = "pending_started_at"
	pendingMetaUpdatedAt = "pending_updated_at"
	pendingMetaBackendID = "pending_backend_id"
)

type Backend struct {
	app          *app.App
	backendID    string
	subs         *fanout
	mu           sync.Mutex
	cancel       map[string]context.CancelFunc
	pending      map[string]pendingTurn
	reusePending map[string]string
}

func newBackend(a *app.App) *Backend {
	return &Backend{
		app:          a,
		backendID:    uuid.NewString(),
		subs:         newFanout(),
		cancel:       map[string]context.CancelFunc{},
		pending:      map[string]pendingTurn{},
		reusePending: map[string]string{},
	}
}

type pendingTurn struct {
	SpaceID         string
	MessageID       string
	ParentMessageID string
	AgentID         string
	StreamID        string
	SessionID       string
	Source          string
	StartedAt       time.Time
}

func (b *Backend) hasActiveTurn(ids ...string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if b.cancel[id] != nil {
			return true
		}
	}
	return false
}

func (b *Backend) WorkspaceInfo() WorkspaceState {
	cfg := b.app.Config()
	current := b.app.CurrentModel()
	provider, model := splitModel(current)
	return WorkspaceState{
		Workspace: cfg.Workspace,
		Provider:  provider,
		Model:     model,
		Runtime:   cfg.Runtime,
		Ready:     provider != "" && provider != "(unconfigured)",
		DataDir:   cfg.DataRoot(),
	}
}

func (b *Backend) SendMessage(req SendRequest) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.cancel[req.SessionID] = cancel
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.cancel, req.SessionID)
		b.mu.Unlock()
		cancel()
	}()
	source := desktopSource
	var sp *space.Space
	var parentMessageID string
	messageCountBefore := -1
	if loaded, err := b.spaceLoader().Load(req.SessionID); err == nil && loaded != nil {
		sp = loaded
		messageCountBefore = len(sp.Messages)
		switch sp.Kind {
		case space.KindChannel:
			source = "desktop:channel:" + sp.ID
		case space.KindDirectChat:
			if isDefaultSumiDirect(sp) {
				source = desktopSource
			} else {
				source = "desktop:direct:" + sp.ID
			}
		case space.KindAgentDM:
			source = "desktop:agent:" + sp.ID
		}
	} else if strings.HasPrefix(req.SessionID, "desktop:agent:") {
		source = req.SessionID
	} else if isThreadID(req.SessionID) {
		if _, err := b.app.SwitchSession(desktopSource, req.SessionID); err != nil {
			return "", err
		}
	}
	parentID := strings.TrimSpace(req.ParentMessageID)
	if parentID != "" {
		if sp == nil || sp.Kind == space.KindAgentDM {
			return "", fmt.Errorf("threads are not supported in this Space kind")
		}
		normalized, ok := b.normalizeThreadParentID(sp, parentID)
		if !ok {
			return "", fmt.Errorf("thread parent message %q not found in this Space", parentID)
		}
		parentMessageID = normalized
		ctx = command.WithParentMessage(ctx, normalized)
	}
	var out string
	var err error
	personaID := strings.TrimSpace(req.PersonaID)
	if sp != nil && isDefaultSumiDirect(sp) {
		personaID = ""
	}
	if personaID != "" {
		out, err = b.app.HandleInputAsWithAttachments(ctx, source, personaID, req.Input, req.Attachments)
	} else {
		out, err = b.app.HandleInputWithAttachments(ctx, source, req.Input, req.Attachments)
	}
	if err != nil {
		b.persistSendFailure(sp, parentMessageID, req.Input, messageCountBefore, err)
		return "", err
	}
	b.persistCommandOutput(sp, parentMessageID, req.Input, out)
	return out, nil
}

func (b *Backend) RetryMessage(req RetryMessageRequest) (string, error) {
	spaceID := strings.TrimSpace(req.SpaceID)
	messageID := strings.TrimSpace(req.MessageID)
	if spaceID == "" || messageID == "" {
		return "", fmt.Errorf("space_id and message_id required")
	}
	sp, err := b.spaceLoader().Load(spaceID)
	if err != nil || sp == nil {
		return "", fmt.Errorf("conversation not found: %s", spaceID)
	}
	idx := -1
	var failed space.Message
	for i, m := range sp.Messages {
		if m.ID == messageID {
			idx = i
			failed = m
			break
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("message not found: %s", messageID)
	}
	if failed.AuthorKind == space.ParticipantUser {
		return "", fmt.Errorf("user messages cannot be retried")
	}
	if failed.Status != "failed" && failed.Status != "pending" {
		return "", fmt.Errorf("message is not retryable")
	}
	if retryPlaceholderSuperseded(sp.Messages, idx, failed) {
		_ = b.app.Spaces().DeleteMessage(sp.ID, failed.ID)
		return "", fmt.Errorf("message already has a successful reply")
	}
	prev, ok := previousUserMessage(sp.Messages[:idx], failed.ParentMessageID)
	if !ok {
		return "", fmt.Errorf("no user message found to retry")
	}
	source := contextSourceForSpace(sp)
	personaID := ""
	if sp.Kind == space.KindAgentDM {
		personaID = space.AgentParticipantID(sp)
	} else if failed.AuthorKind == space.ParticipantAgent && failed.AuthorID != "assistant" && b.app.Personas().Get(failed.AuthorID) != nil {
		personaID = failed.AuthorID
	}
	key := pendingKey(sp.ID, failed.ParentMessageID, fallback(personaID, failed.AuthorID))
	b.mu.Lock()
	b.reusePending[key] = failed.ID
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.reusePending, key)
		delete(b.cancel, sp.ID)
		b.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.cancel[sp.ID] = cancel
	b.mu.Unlock()
	defer cancel()
	if failed.ParentMessageID != "" {
		ctx = command.WithParentMessage(ctx, failed.ParentMessageID)
	}
	var out string
	if sp.Kind == space.KindChannel && personaID != "" {
		out, err = b.app.RetryChannelAgentReply(ctx, source, sp.ID, personaID, failed.ParentMessageID, prev.ID, prev.Content)
	} else {
		out, err = b.app.HandleExistingUserInput(ctx, source, personaID, prev.Content, prev.ID)
	}
	if err != nil {
		_, _ = b.app.Spaces().UpdateMessage(sp.ID, failed.ID, func(m *space.Message) {
			m.Status = "failed"
			m.Error = err.Error()
		})
		return "", err
	}
	return out, nil
}

func previousUserMessage(messages []space.Message, parentMessageID string) (space.Message, bool) {
	parentMessageID = strings.TrimSpace(parentMessageID)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if strings.TrimSpace(m.ParentMessageID) != parentMessageID {
			continue
		}
		if m.AuthorKind == space.ParticipantUser && strings.TrimSpace(m.Content) != "" {
			return m, true
		}
	}
	return space.Message{}, false
}

func (b *Backend) persistSendFailure(sp *space.Space, parentMessageID, input string, messageCountBefore int, sendErr error) {
	if sp == nil || sendErr == nil {
		return
	}
	if b.hasFailedPendingMessage(sp.ID, parentMessageID) {
		return
	}
	if messageCountBefore >= 0 {
		if latest, err := b.app.Spaces().LoadSpace(sp.ID); err == nil && latest != nil && len(latest.Messages) <= messageCountBefore {
			_, _, _ = b.app.Spaces().AppendMessageWithRouting(sp.ID, space.Message{
				AuthorID:        b.app.Spaces().UserParticipant().ID,
				AuthorKind:      space.ParticipantUser,
				Content:         input,
				ParentMessageID: strings.TrimSpace(parentMessageID),
			}, nil, nil)
		}
	}
	_, _, _ = b.app.Spaces().AppendMessageWithRouting(sp.ID, space.Message{
		AuthorID:        "sumi",
		AuthorKind:      space.ParticipantSystem,
		Content:         "Send failed: " + sendErr.Error(),
		ParentMessageID: strings.TrimSpace(parentMessageID),
	}, nil, nil)
}

func (b *Backend) persistCommandOutput(sp *space.Space, parentMessageID, input, output string) {
	if sp == nil {
		return
	}
	b.persistCommandOutputForSpace(sp.ID, parentMessageID, input, output)
}

func (b *Backend) persistCommandOutputForSpace(spaceID, parentMessageID, input, output string) {
	if strings.TrimSpace(spaceID) == "" || strings.TrimSpace(output) == "" || !b.app.IsRegisteredCommandInput(input) {
		return
	}
	_, _, _ = b.app.Spaces().AppendMessageWithRouting(spaceID, space.Message{
		AuthorID:        "sumi",
		AuthorKind:      space.ParticipantSystem,
		Content:         strings.TrimSpace(output),
		ParentMessageID: strings.TrimSpace(parentMessageID),
	}, nil, nil)
}

func (b *Backend) hasFailedPendingMessage(spaceID, parentMessageID string) bool {
	sp, err := b.spaceLoader().Load(spaceID)
	if err != nil || sp == nil {
		return false
	}
	parentMessageID = strings.TrimSpace(parentMessageID)
	for i := len(sp.Messages) - 1; i >= 0; i-- {
		m := sp.Messages[i]
		if strings.TrimSpace(m.ParentMessageID) != parentMessageID {
			continue
		}
		if m.AuthorKind == space.ParticipantUser {
			return false
		}
		return m.Status == "failed"
	}
	return false
}

func pendingKey(spaceID, parentMessageID, agentID string) string {
	return strings.TrimSpace(spaceID) + "\x00" + strings.TrimSpace(parentMessageID) + "\x00" + strings.TrimSpace(agentID)
}

func (b *Backend) trackTurnEvent(ev bus.Event) bus.Event {
	if b == nil || b.app == nil || b.app.Spaces() == nil || strings.TrimSpace(ev.SpaceID) == "" || strings.TrimSpace(ev.StreamID) == "" {
		return ev
	}
	switch ev.Type {
	case bus.TurnStarted:
		return b.trackTurnStarted(ev)
	case bus.TurnChunk, bus.TurnReasoning:
		b.updatePendingFromChunk(ev)
	case bus.TurnError:
		b.failPendingTurn(ev)
	case bus.TurnFinished:
		b.finishPendingTurn(ev)
	}
	return ev
}

func (b *Backend) trackTurnStarted(ev bus.Event) bus.Event {
	agentID := strings.TrimSpace(ev.AgentID)
	if agentID == "" {
		agentID = "assistant"
	}
	key := pendingKey(ev.SpaceID, ev.ParentMessageID, agentID)
	b.mu.Lock()
	reuseID := b.reusePending[key]
	delete(b.reusePending, key)
	b.mu.Unlock()
	var written space.Message
	var err error
	if reuseID != "" {
		written, err = b.app.Spaces().UpdateMessage(ev.SpaceID, reuseID, func(m *space.Message) {
			m.AuthorID = agentID
			m.AuthorKind = space.ParticipantAgent
			m.Content = ""
			m.Reasoning = ""
			m.Status = "pending"
			m.Error = ""
			m.ParentMessageID = strings.TrimSpace(ev.ParentMessageID)
			m.RuntimeMeta = b.pendingRuntimeMeta(ev, time.Now())
			m.Usage = nil
		})
	} else {
		written, _, err = b.app.Spaces().AppendMessageWithRouting(ev.SpaceID, space.Message{
			AuthorID:        agentID,
			AuthorKind:      space.ParticipantAgent,
			Status:          "pending",
			ParentMessageID: strings.TrimSpace(ev.ParentMessageID),
			RuntimeMeta:     b.pendingRuntimeMeta(ev, time.Now()),
		}, nil, nil)
	}
	if err != nil || written.ID == "" {
		return ev
	}
	b.mu.Lock()
	b.pending[ev.StreamID] = pendingTurn{
		SpaceID:         ev.SpaceID,
		MessageID:       written.ID,
		ParentMessageID: strings.TrimSpace(ev.ParentMessageID),
		AgentID:         agentID,
		StreamID:        ev.StreamID,
		SessionID:       ev.SessionID,
		Source:          ev.Source,
		StartedAt:       time.Now(),
	}
	b.mu.Unlock()
	ev.MessageID = written.ID
	return ev
}

func (b *Backend) updatePendingFromChunk(ev bus.Event) {
	b.mu.Lock()
	p := b.pending[ev.StreamID]
	b.mu.Unlock()
	if p.MessageID == "" {
		return
	}
	text := ev.Text
	if text == "" {
		return
	}
	_, _ = b.app.Spaces().UpdateMessage(p.SpaceID, p.MessageID, func(m *space.Message) {
		if ev.Type == bus.TurnReasoning {
			m.Reasoning += text
		} else {
			m.Content += text
		}
		m.Status = "pending"
		if m.RuntimeMeta == nil {
			m.RuntimeMeta = map[string]string{}
		}
		m.RuntimeMeta[pendingMetaUpdatedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	})
}

func (b *Backend) failPendingTurn(ev bus.Event) {
	b.mu.Lock()
	p := b.pending[ev.StreamID]
	delete(b.pending, ev.StreamID)
	b.mu.Unlock()
	if p.MessageID == "" {
		return
	}
	errText := strings.TrimSpace(ev.Err)
	if errText == "" {
		errText = "Agent reply interrupted."
	}
	_, _ = b.app.Spaces().UpdateMessage(p.SpaceID, p.MessageID, func(m *space.Message) {
		m.Status = "failed"
		m.Error = errText
	})
}

func (b *Backend) finishPendingTurn(ev bus.Event) {
	b.mu.Lock()
	p := b.pending[ev.StreamID]
	delete(b.pending, ev.StreamID)
	b.mu.Unlock()
	if p.MessageID == "" {
		return
	}
	_ = b.app.Spaces().DeleteMessage(p.SpaceID, p.MessageID)
}

func (b *Backend) recoverPendingMessages(sp *space.Space) *space.Space {
	if sp == nil {
		return sp
	}
	changed := false
	now := time.Now()
	for i := range sp.Messages {
		if normalized, ok := normalizeLegacyRuntimeError(sp.Messages[i].Error); ok {
			sp.Messages[i].Error = normalized
			changed = true
		}
		if sp.Messages[i].Status != "pending" {
			continue
		}
		if b.messageStillRunning(sp, sp.Messages[i]) {
			continue
		}
		if pendingFromDifferentBackend(sp.Messages[i], b.backendID) {
			sp.Messages[i].Status = "failed"
			sp.Messages[i].Error = "Agent reply was interrupted because the desktop backend restarted. Retry to run this message again."
			changed = true
			continue
		}
		if pendingRecentlyTouched(sp.Messages[i], now) {
			continue
		}
		sp.Messages[i].Status = "failed"
		sp.Messages[i].Error = "Agent reply was interrupted. Retry to run this message again."
		changed = true
	}
	if dropSupersededRetryPlaceholders(sp) {
		changed = true
	}
	if dropLegacyRoutedFailureNotices(sp) {
		changed = true
	}
	if !changed {
		return sp
	}
	if err := b.app.Spaces().Store().SaveSpace(sp); err != nil {
		return sp
	}
	return sp
}

func dropSupersededRetryPlaceholders(sp *space.Space) bool {
	if sp == nil || len(sp.Messages) == 0 {
		return false
	}
	next := sp.Messages[:0]
	changed := false
	for i, m := range sp.Messages {
		if retryPlaceholderSuperseded(sp.Messages, i, m) {
			changed = true
			continue
		}
		next = append(next, m)
	}
	if !changed {
		return false
	}
	sp.Messages = next
	return true
}

func dropLegacyRoutedFailureNotices(sp *space.Space) bool {
	if sp == nil || len(sp.Messages) == 0 {
		return false
	}
	next := sp.Messages[:0]
	changed := false
	for _, m := range sp.Messages {
		if legacyRoutedFailureNotice(m) {
			changed = true
			continue
		}
		next = append(next, m)
	}
	if !changed {
		return false
	}
	sp.Messages = next
	return true
}

func legacyRoutedFailureNotice(m space.Message) bool {
	if m.AuthorKind != space.ParticipantSystem {
		return false
	}
	content := strings.TrimSpace(m.Content)
	if !strings.HasPrefix(content, "@") {
		return false
	}
	return strings.Contains(content, " failed:")
}

func normalizeLegacyRuntimeError(errText string) (string, bool) {
	raw := strings.TrimSpace(errText)
	if raw == "" {
		return "", false
	}
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "failed to connect to websocket") {
		return raw, false
	}
	summary := "failed to connect to websocket"
	if strings.Contains(lower, "connection reset by peer") {
		summary = "failed to connect to websocket: connection reset by peer"
	}
	prefix := ""
	if idx := strings.Index(raw, ":"); idx > 0 {
		head := strings.TrimSpace(raw[:idx])
		if strings.Contains(strings.ToLower(head), "exited") {
			prefix = head + ": "
		}
	}
	normalized := prefix + summary
	return normalized, normalized != raw
}

func retryPlaceholderSuperseded(messages []space.Message, idx int, m space.Message) bool {
	if idx < 0 || idx >= len(messages) || m.AuthorKind != space.ParticipantAgent {
		return false
	}
	if m.Status != "failed" && m.Status != "pending" {
		return false
	}
	parentID := strings.TrimSpace(m.ParentMessageID)
	authorID := strings.TrimSpace(m.AuthorID)
	for i := idx + 1; i < len(messages); i++ {
		next := messages[i]
		if strings.TrimSpace(next.ParentMessageID) != parentID {
			continue
		}
		if next.AuthorKind == space.ParticipantUser {
			return false
		}
		if next.AuthorKind != space.ParticipantAgent {
			continue
		}
		if strings.TrimSpace(next.AuthorID) != authorID {
			continue
		}
		if next.Status == "failed" || next.Status == "pending" {
			continue
		}
		if strings.TrimSpace(next.Content) != "" || strings.TrimSpace(next.Reasoning) != "" {
			return true
		}
	}
	return false
}

func (b *Backend) messageStillRunning(sp *space.Space, m space.Message) bool {
	if sp == nil {
		return false
	}
	keys := []string{sp.ID, contextSourceForSpace(sp)}
	if m.AuthorID != "" {
		keys = append(keys, "desktop:agent:"+sp.ID, "desktop:agent:"+m.AuthorID)
	}
	return b.hasActiveTurn(keys...)
}

func (b *Backend) pendingRuntimeMeta(ev bus.Event, now time.Time) map[string]string {
	meta := map[string]string{
		pendingMetaStartedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if b != nil && strings.TrimSpace(b.backendID) != "" {
		meta[pendingMetaBackendID] = b.backendID
	}
	if streamID := strings.TrimSpace(ev.StreamID); streamID != "" {
		meta[pendingMetaStreamID] = streamID
	}
	if sessionID := strings.TrimSpace(ev.SessionID); sessionID != "" {
		meta[pendingMetaSessionID] = sessionID
	}
	if source := strings.TrimSpace(ev.Source); source != "" {
		meta[pendingMetaSource] = source
	}
	return meta
}

func pendingFromDifferentBackend(m space.Message, backendID string) bool {
	stored := strings.TrimSpace(m.RuntimeMeta[pendingMetaBackendID])
	current := strings.TrimSpace(backendID)
	return stored != "" && current != "" && stored != current
}

func pendingRecentlyTouched(m space.Message, now time.Time) bool {
	touched := m.CreatedAt
	for _, key := range []string{pendingMetaStartedAt, pendingMetaUpdatedAt} {
		ts, ok := parsePendingMetaTime(m.RuntimeMeta[key])
		if ok && (touched.IsZero() || ts.After(touched)) {
			touched = ts
		}
	}
	if touched.IsZero() {
		return false
	}
	return now.Sub(touched) < pendingRecoveryGrace
}

func parsePendingMetaTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (b *Backend) normalizeThreadParentID(sp *space.Space, parentID string) (string, bool) {
	target, ok := findMessage(sp.Messages, parentID)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(target.ParentMessageID) != "" {
		return target.ParentMessageID, true
	}
	return target.ID, true
}

func (b *Backend) StopTurn(sessionID string) error {
	b.mu.Lock()
	cancel := b.cancel[sessionID]
	delete(b.cancel, sessionID)
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (b *Backend) DeleteConversation(req DeleteConversationRequest) (DeleteConversationResult, error) {
	kind := strings.TrimSpace(req.Kind)
	id := strings.TrimSpace(req.ID)
	parentID := strings.TrimSpace(req.ParentMessageID)
	if id == "" {
		return DeleteConversationResult{}, fmt.Errorf("conversation id required")
	}
	if kind == "thread" {
		return b.deleteThreadConversation(id, parentID)
	}
	sp, err := b.app.Spaces().LoadSpace(id)
	if err != nil || sp == nil {
		if space.IsSpaceID(id) {
			return DeleteConversationResult{OK: true}, nil
		}
		return DeleteConversationResult{}, fmt.Errorf("conversation not found: %s", id)
	}
	if !deleteKindMatchesSpace(kind, sp.Kind) {
		return DeleteConversationResult{}, fmt.Errorf("conversation kind %q does not match %q", kind, sp.Kind)
	}
	sources := conversationSessionSources(sp)
	b.stopConversationTurns(append([]string{sp.ID}, sources...)...)
	deletedSessions, err := b.app.DeleteSessionsMatching(func(s *session.Session) bool {
		return sessionSourceMatches(s.Source, sources)
	})
	if err != nil {
		return DeleteConversationResult{}, err
	}
	deletedTasks := 0
	if b.app.Tasks() != nil {
		deletedTasks, err = b.app.Tasks().DeleteBySpace(sp.ID)
		if err != nil {
			return DeleteConversationResult{}, err
		}
	}
	if err := b.app.Spaces().DeleteSpace(sp.ID); err != nil {
		return DeleteConversationResult{}, err
	}
	return DeleteConversationResult{
		OK:              true,
		DeletedSpace:    true,
		DeletedSessions: deletedSessions,
		DeletedTasks:    deletedTasks,
	}, nil
}

func (b *Backend) deleteThreadConversation(spaceID, parentID string) (DeleteConversationResult, error) {
	if parentID == "" {
		return DeleteConversationResult{}, fmt.Errorf("parent message id required")
	}
	sp, err := b.app.Spaces().LoadSpace(spaceID)
	if err != nil || sp == nil {
		if space.IsSpaceID(spaceID) {
			return DeleteConversationResult{OK: true}, nil
		}
		return DeleteConversationResult{}, fmt.Errorf("conversation not found: %s", spaceID)
	}
	if sp.Kind == space.KindAgentDM {
		return DeleteConversationResult{}, fmt.Errorf("threads are not supported in this Space kind")
	}
	if _, ok := findMessage(sp.Messages, parentID); !ok {
		return DeleteConversationResult{}, fmt.Errorf("thread parent message %q not found", parentID)
	}
	sources := threadSessionSources(sp, parentID)
	b.stopConversationTurns(sources...)
	deletedSessions, err := b.app.DeleteSessionsMatching(func(s *session.Session) bool {
		return sessionSourceMatches(s.Source, sources)
	})
	if err != nil {
		return DeleteConversationResult{}, err
	}
	deletedTasks := 0
	if b.app.Tasks() != nil {
		deletedTasks, err = b.app.Tasks().DeleteByThread(sp.ID, parentID)
		if err != nil {
			return DeleteConversationResult{}, err
		}
	}
	if err := b.app.Spaces().DeleteThread(sp.ID, parentID); err != nil {
		return DeleteConversationResult{}, err
	}
	return DeleteConversationResult{
		OK:              true,
		DeletedSessions: deletedSessions,
		DeletedTasks:    deletedTasks,
	}, nil
}

func (b *Backend) InspectContext(req ContextInspectRequest) (ContextInspectView, error) {
	normalized, err := b.normalizeContextRequest(req)
	if err != nil {
		return ContextInspectView{}, err
	}
	view, err := b.app.InspectContext(app.ContextInspectInput{
		SpaceID:         normalized.SpaceID,
		Source:          normalized.Source,
		SessionSource:   normalized.SessionSource,
		ParentMessageID: normalized.ParentMessageID,
		AgentID:         normalized.AgentID,
		Profile:         app.ContextProfile(normalized.Profile),
	})
	if err != nil {
		return ContextInspectView{}, err
	}
	return desktopContextInspectView(view), nil
}

func (b *Backend) ResetContext(req ContextResetRequest) (ContextResetResult, error) {
	normalized, err := b.normalizeContextRequest(ContextInspectRequest{
		SpaceID:         req.SpaceID,
		Source:          req.Source,
		SessionSource:   req.SessionSource,
		ParentMessageID: req.ParentMessageID,
		AgentID:         req.AgentID,
	})
	if err != nil {
		return ContextResetResult{}, err
	}
	res, err := b.app.ResetContext(app.ContextResetInput{
		SpaceID:         normalized.SpaceID,
		Source:          normalized.Source,
		SessionSource:   normalized.SessionSource,
		ParentMessageID: normalized.ParentMessageID,
		AgentID:         normalized.AgentID,
		Action:          req.Action,
	})
	if err != nil {
		return ContextResetResult{}, err
	}
	return ContextResetResult{
		OK:                     res.OK,
		Action:                 res.Action,
		Source:                 res.Source,
		SessionSource:          res.SessionSource,
		PreviousSessionID:      res.PreviousSessionID,
		SessionID:              res.SessionID,
		ClearedSummary:         res.ClearedSummary,
		RemovedSummaryMessages: res.RemovedSummaryMessages,
		Note:                   res.Note,
	}, nil
}

func (b *Backend) normalizeContextRequest(req ContextInspectRequest) (ContextInspectRequest, error) {
	req.SpaceID = strings.TrimSpace(req.SpaceID)
	req.Source = strings.TrimSpace(req.Source)
	req.SessionSource = strings.TrimSpace(req.SessionSource)
	req.ParentMessageID = strings.TrimSpace(req.ParentMessageID)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Profile = strings.TrimSpace(req.Profile)
	if req.SpaceID == "" {
		return req, nil
	}
	sp, err := b.spaceLoader().Load(req.SpaceID)
	if err != nil || sp == nil {
		return ContextInspectRequest{}, fmt.Errorf("space not found: %s", req.SpaceID)
	}
	if req.Source == "" {
		req.Source = contextSourceForSpace(sp)
	}
	if req.AgentID == "" && sp.Kind == space.KindAgentDM {
		req.AgentID = space.AgentParticipantID(sp)
	}
	if req.Profile == "" {
		req.Profile = string(contextProfileForSpace(sp, req.ParentMessageID, req.Source))
	}
	return req, nil
}

func contextSourceForSpace(sp *space.Space) string {
	if sp == nil {
		return desktopSource
	}
	switch sp.Kind {
	case space.KindChannel:
		return "desktop:channel:" + sp.ID
	case space.KindAgentDM:
		return "desktop:agent:" + sp.ID
	case space.KindDirectChat:
		if isDefaultSumiDirect(sp) {
			return desktopSource
		}
		return "desktop:direct:" + sp.ID
	default:
		return desktopSource
	}
}

func contextProfileForSpace(sp *space.Space, parentMessageID, source string) app.ContextProfile {
	if strings.TrimSpace(parentMessageID) != "" {
		return app.ContextProfileThread
	}
	if strings.HasPrefix(strings.TrimSpace(source), "tg:") {
		return app.ContextProfileTelegram
	}
	if sp == nil {
		return app.ContextProfileDirect
	}
	switch sp.Kind {
	case space.KindAgentDM:
		return app.ContextProfileAgentDM
	case space.KindChannel:
		return app.ContextProfileChannel
	default:
		return app.ContextProfileDirect
	}
}

func desktopContextInspectView(v app.ContextInspectView) ContextInspectView {
	out := ContextInspectView{
		Profile:         string(v.Profile),
		Source:          v.Source,
		SessionSource:   v.SessionSource,
		SessionID:       v.SessionID,
		SpaceID:         v.SpaceID,
		ParentMessageID: v.ParentMessageID,
		AgentID:         v.AgentID,
		TokenLimit:      v.TokenLimit,
		RawMessageCount: v.RawMessageCount,
		EligibleCount:   v.EligibleCount,
		SelectedCount:   v.SelectedCount,
		SummarizedCount: v.SummarizedCount,
		Summary:         v.Summary,
		SessionSummary:  v.SessionSummary,
		Notes:           append([]string(nil), v.Notes...),
		Messages:        make([]ContextInspectMessage, 0, len(v.Messages)),
	}
	for _, c := range v.FilteredCounts {
		out.FilteredCounts = append(out.FilteredCounts, ContextFilteredCount{Reason: c.Reason, Count: c.Count})
	}
	for _, m := range v.Messages {
		out.Messages = append(out.Messages, ContextInspectMessage{
			ID:        m.ID,
			Role:      m.Role,
			AuthorID:  m.AuthorID,
			Content:   m.Content,
			Tokens:    m.Tokens,
			CreatedAt: m.CreatedAt,
		})
	}
	return out
}

func (b *Backend) stopConversationTurns(ids ...string) {
	for _, id := range ids {
		_ = b.StopTurn(id)
	}
}

func deleteKindMatchesSpace(kind string, spaceKind space.Kind) bool {
	switch strings.TrimSpace(kind) {
	case "", string(spaceKind):
		return true
	case "direct":
		return spaceKind == space.KindDirectChat
	case "agent":
		return spaceKind == space.KindAgentDM
	case "channel":
		return spaceKind == space.KindChannel
	}
	return false
}

func conversationSessionSources(sp *space.Space) []string {
	if sp == nil {
		return nil
	}
	switch sp.Kind {
	case space.KindChannel:
		return []string{
			"desktop:channel:" + sp.ID,
			"cli:channel:" + sp.ID,
		}
	case space.KindDirectChat:
		if isDefaultSumiDirect(sp) {
			return []string{
				desktopSource,
				"desktop:direct:" + sp.ID,
			}
		}
		return []string{"desktop:direct:" + sp.ID}
	case space.KindAgentDM:
		out := []string{"desktop:agent:" + sp.ID}
		if isDefaultAgentDM(sp) {
			if pid := space.AgentParticipantID(sp); pid != "" {
				out = append(out, "desktop:agent:"+pid, "cli:agent:"+pid)
			}
		}
		return out
	default:
		return nil
	}
}

func threadSessionSources(sp *space.Space, parentID string) []string {
	parentID = strings.TrimSpace(parentID)
	if sp == nil || parentID == "" {
		return nil
	}
	bases := conversationSessionSources(sp)
	out := make([]string, 0, len(bases))
	for _, base := range bases {
		if strings.TrimSpace(base) == "" {
			continue
		}
		out = append(out, base+":thread:"+parentID)
	}
	return out
}

func sessionSourceMatches(source string, candidates []string) bool {
	source = strings.TrimSpace(source)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if candidate == desktopSource {
			if source == desktopSource || strings.HasPrefix(source, desktopSource+":persona:") {
				return true
			}
			continue
		}
		if source == candidate || strings.HasPrefix(source, candidate+":") {
			return true
		}
	}
	return false
}

func (b *Backend) NewDirectChat(title, agentID string) (SessionDetail, error) {
	seed := newDirectChatSeed()
	sp, err := b.app.Spaces().EnsureSpace(space.KindDirectChat, seed, space.PersonaInfo{})
	if err != nil {
		return SessionDetail{}, err
	}
	if title = strings.TrimSpace(title); title != "" {
		if err := b.app.Spaces().UpdateTitle(sp.ID, title); err != nil {
			return SessionDetail{}, err
		}
		if updated, err := b.app.Spaces().LoadSpace(sp.ID); err == nil && updated != nil {
			sp = updated
		}
	}
	if agentID = strings.TrimSpace(agentID); agentID != "" {
		p := b.app.Personas().Get(agentID)
		if p == nil {
			return SessionDetail{}, fmt.Errorf("persona not registered: %s", agentID)
		}
		if err := b.app.Spaces().AddAgentParticipant(sp.ID, space.PersonaInfo{
			ID:      p.ID,
			Display: p.Display,
			Role:    p.Description,
		}); err != nil {
			return SessionDetail{}, err
		}
		if err := b.app.Spaces().SetAgentMode(sp.ID, agentID, "listen"); err != nil {
			return SessionDetail{}, err
		}
		if updated, err := b.app.Spaces().LoadSpace(sp.ID); err == nil && updated != nil {
			sp = updated
		}
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        directChatTitle(sp),
			TitleFixed:   isDefaultSumiDirect(sp),
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
		},
		ActiveRuns: b.pendingActiveRuns(sp.ID, ""),
		Messages:   spaceMessagesToView(sp, b.app),
	}, nil
}

func (b *Backend) UpdateDirectChatTitle(spaceID, title string) (DirectChatItem, error) {
	spaceID = strings.TrimSpace(spaceID)
	title = strings.TrimSpace(title)
	if spaceID == "" || title == "" {
		return DirectChatItem{}, fmt.Errorf("space id and title required")
	}
	sp, err := b.spaceLoader().LoadTyped(spaceID, space.KindDirectChat)
	if err != nil {
		return DirectChatItem{}, err
	}
	if sp == nil {
		return DirectChatItem{}, fmt.Errorf("direct chat not found: %s", spaceID)
	}
	if isDefaultSumiDirect(sp) {
		return DirectChatItem{}, fmt.Errorf("default direct chat title is fixed")
	}
	if err := b.app.Spaces().UpdateTitle(sp.ID, title); err != nil {
		return DirectChatItem{}, err
	}
	updated, err := b.app.Spaces().LoadSpace(sp.ID)
	if err != nil {
		return DirectChatItem{}, err
	}
	return DirectChatItem{
		ID:         updated.ID,
		Kind:       "direct_chat",
		Title:      directChatTitle(updated),
		TitleFixed: isDefaultSumiDirect(updated),
		Agents:     directChatAgentIDs(updated),
		UpdatedAt:  updated.UpdatedAt,
	}, nil
}

func (b *Backend) ListDirectChats() []DirectChatItem {
	if _, err := b.ensureDefaultSumiDirect(); err != nil {
		return []DirectChatItem{}
	}
	spaces, err := b.app.Spaces().ListSpaces()
	if err != nil {
		return []DirectChatItem{}
	}
	type entry struct {
		sp *space.Space
	}
	all := make([]entry, 0)
	for _, sp := range spaces {
		if sp.Kind != space.KindDirectChat {
			continue
		}
		all = append(all, entry{sp: sp})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].sp.UpdatedAt.After(all[j].sp.UpdatedAt) })

	out := make([]DirectChatItem, 0, len(all))
	for _, e := range all {
		out = append(out, DirectChatItem{
			ID:         e.sp.ID,
			Kind:       "direct_chat",
			Title:      directChatTitle(e.sp),
			TitleFixed: isDefaultSumiDirect(e.sp),
			Agents:     directChatAgentIDs(e.sp),
			UpdatedAt:  e.sp.UpdatedAt,
			HasRunning: b.directChatHasActiveTurn(e.sp),
		})
	}
	out = append(out, b.defaultAgentDMItems(spaces)...)
	sort.Slice(out, func(i, j int) bool {
		if isDefaultSumiDirectItem(out[i]) != isDefaultSumiDirectItem(out[j]) {
			return isDefaultSumiDirectItem(out[i])
		}
		if out[i].UpdatedAt.IsZero() != out[j].UpdatedAt.IsZero() {
			return !out[i].UpdatedAt.IsZero()
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (b *Backend) ensureDefaultSumiDirect() (*space.Space, error) {
	spaces, err := b.app.Spaces().ListSpaces()
	if err == nil {
		for _, sp := range spaces {
			if isDefaultSumiDirect(sp) {
				return sp, nil
			}
		}
	}
	return b.app.Spaces().EnsureSpace(space.KindDirectChat, defaultSumiDirectTitle, space.PersonaInfo{})
}

func isDefaultSumiDirect(sp *space.Space) bool {
	return sp != nil &&
		sp.Kind == space.KindDirectChat &&
		strings.EqualFold(strings.TrimSpace(sp.Title), defaultSumiDirectTitle)
}

func isDefaultSumiDirectItem(item DirectChatItem) bool {
	return item.Kind == "direct_chat" && (item.TitleFixed || strings.EqualFold(strings.TrimSpace(item.Title), defaultSumiDirectTitle))
}

func directChatAgentIDs(sp *space.Space) []string {
	if isDefaultSumiDirect(sp) {
		return []string{}
	}
	return spaceAgentIDs(sp)
}

func (b *Backend) defaultAgentDMItems(spaces []*space.Space) []DirectChatItem {
	out := make([]DirectChatItem, 0)
	for _, sp := range spaces {
		if sp.Kind != space.KindAgentDM || !isDefaultAgentDM(sp) {
			continue
		}
		pid := space.AgentParticipantID(sp)
		if pid == "" {
			continue
		}
		if b.app.Personas().Get(pid) == nil {
			continue
		}
		display := pid
		if p := b.app.Personas().Get(pid); p != nil {
			display = p.Display
		}
		item := DirectChatItem{
			ID:          sp.ID,
			Kind:        "agent_dm",
			PersonaID:   pid,
			PersonaName: display,
			Title:       "@" + fallback(display, pid),
			Agents:      []string{pid},
			UpdatedAt:   sp.UpdatedAt,
			HasRunning: b.hasActiveTurn(
				sp.ID,
				"desktop:agent:"+sp.ID,
				"desktop:agent:"+pid,
			),
		}
		out = append(out, item)
	}
	return out
}

func (b *Backend) directChatHasActiveTurn(sp *space.Space) bool {
	if sp == nil {
		return false
	}
	if b.hasActiveTurn(sp.ID, "desktop:direct:"+sp.ID) {
		return true
	}
	return isDefaultSumiDirect(sp) && b.hasActiveTurn(desktopSource)
}

func (b *Backend) GetDirectChat(id string) SessionDetail {
	sp, err := b.spaceLoader().LoadTyped(id, space.KindDirectChat)
	if err != nil || sp == nil {
		return SessionDetail{}
	}
	sp = b.recoverPendingMessages(sp)
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        directChatTitle(sp),
			TitleFixed:   isDefaultSumiDirect(sp),
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
			Running:      b.directChatHasActiveTurn(sp),
		},
		ActiveRuns: b.pendingActiveRuns(sp.ID, ""),
		Messages:   spaceMessagesToView(sp, b.app),
	}
}

func directChatTitle(sp *space.Space) string {
	if sp == nil {
		return "New chat"
	}
	if isDefaultSumiDirect(sp) {
		return defaultSumiDirectTitle
	}
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser {
			if t := strings.TrimSpace(m.Content); t != "" {
				return preview(t, 48)
			}
		}
	}
	if t := strings.TrimSpace(sp.Title); t != "" && !strings.HasPrefix(t, "dchat-") {
		return t
	}
	return "New chat"
}

func newDirectChatSeed() string {
	return "dchat-" + time.Now().Format("20060102-150405") + "-" + uuid.NewString()[:4]
}

func (b *Backend) ListChannels() []ChannelItem {
	cfg := b.app.Config()
	spaces, err := b.app.Spaces().Store().ListSpaces()
	if err == nil {
		out := make([]ChannelItem, 0, 1)
		for _, sp := range spaces {
			if sp.Kind != space.KindChannel {
				continue
			}
			out = append(out, ChannelItem{
				ID:         sp.ID,
				Name:       channelDisplayName(sp, cfg.Workspace),
				Topic:      "",
				Agents:     spaceAgentIDs(sp),
				AgentModes: sp.AgentModes,
				UpdatedAt:  sp.UpdatedAt,
			})
		}
		if len(out) > 0 {
			sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
			return out
		}
	}
	return []ChannelItem{
		{
			ID:        defaultChannelID,
			Name:      workspaceName(cfg.Workspace),
			Topic:     "",
			Agents:    personaIDs(b.app),
			UpdatedAt: time.Now(),
		},
	}
}

func (b *Backend) ListRecent() []RecentItem {
	spaces, err := b.app.Spaces().ListSpaces()
	if err != nil {
		return []RecentItem{}
	}
	cfg := b.app.Config()
	out := make([]RecentItem, 0, len(spaces))
	for _, sp := range spaces {
		var item RecentItem
		switch sp.Kind {
		case space.KindChannel:
			item = RecentItem{
				ID:        sp.ID,
				Kind:      "channel",
				Title:     "#" + channelDisplayName(sp, cfg.Workspace),
				Subtitle:  recentSubtitle(sp),
				UpdatedAt: sp.UpdatedAt,
			}
		case space.KindDirectChat:
			item = RecentItem{
				ID:        sp.ID,
				Kind:      "direct_chat",
				Title:     directChatTitle(sp),
				Subtitle:  recentSubtitle(sp),
				UpdatedAt: sp.UpdatedAt,
			}
		case space.KindAgentDM:
			pid := space.AgentParticipantID(sp)
			display := pid
			if p := b.app.Personas().Get(pid); p != nil {
				display = p.Display
			}
			title := visibleAgentDMTitle(sp, pid)
			if title == "New chat" || isDefaultAgentDM(sp) {
				title = "@" + fallback(display, pid)
			}
			item = RecentItem{
				ID:        sp.ID,
				Kind:      "agent_dm",
				Title:     title,
				Subtitle:  recentSubtitle(sp),
				UpdatedAt: sp.UpdatedAt,
			}
		default:
			continue
		}
		if len(sp.Messages) == 0 {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func recentSubtitle(sp *space.Space) string {
	if sp == nil || len(sp.Messages) == 0 {
		return ""
	}
	last := sp.Messages[len(sp.Messages)-1]
	c := strings.TrimSpace(last.Content)
	if c == "" {
		return ""
	}
	c = strings.ReplaceAll(c, "\n", " ")
	if len([]rune(c)) > 60 {
		r := []rune(c)
		c = string(r[:60]) + "…"
	}
	prefix := ""
	switch last.AuthorKind {
	case space.ParticipantUser:
		prefix = "You: "
	case space.ParticipantAgent:
		display := last.AuthorID
		prefix = display + ": "
	}
	return prefix + c
}
func channelDisplayName(sp *space.Space, workspace string) string {
	if sp == nil {
		return ""
	}
	if strings.TrimSpace(sp.Title) == "" || sp.Title == "default" {
		return workspaceName(workspace)
	}
	return sp.Title
}

func spaceAgentIDs(sp *space.Space) []string {
	if sp == nil {
		return []string{}
	}
	out := make([]string, 0, len(sp.Participants))
	for _, p := range sp.Participants {
		if p.Kind == space.ParticipantAgent {
			out = append(out, p.ID)
		}
	}
	sort.Strings(out)
	return out
}

func (b *Backend) ListThreads() []ThreadItem {
	return []ThreadItem{}
}

func (b *Backend) ListAgents() []AgentItem {
	out := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, personaAgentItem(p, "idle"))
	}
	return out
}

func (b *Backend) CreateChannel(name string) (ChannelItem, error) {
	seed := normalizeChannelSeed(name)
	if seed == "" {
		return ChannelItem{}, fmt.Errorf("channel name required")
	}
	if existing, err := b.app.Spaces().Store().FindSpaceByKindAndSeed(space.KindChannel, seed); err == nil && existing != nil {
		return ChannelItem{}, fmt.Errorf("channel %q already exists", seed)
	}
	sp, err := b.app.Spaces().EnsureSpace(space.KindChannel, seed, space.PersonaInfo{})
	if err != nil {
		return ChannelItem{}, err
	}
	return ChannelItem{
		ID:         sp.ID,
		Name:       channelDisplayName(sp, b.app.Config().Workspace),
		Topic:      "",
		Agents:     spaceAgentIDs(sp),
		AgentModes: sp.AgentModes,
		UpdatedAt:  sp.UpdatedAt,
	}, nil
}

func (b *Backend) SetChannelAgentMode(channelID, personaID, mode string) error {
	return b.app.Spaces().SetAgentMode(channelID, personaID, mode)
}

func (b *Backend) AddAgentToChannel(channelID, personaID string) error {
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return fmt.Errorf("persona id required")
	}
	p := b.app.Personas().Get(personaID)
	if p == nil {
		return fmt.Errorf("persona not registered: %s", personaID)
	}
	return b.app.Spaces().AddAgentParticipant(channelID, space.PersonaInfo{
		ID:      p.ID,
		Display: p.Display,
		Role:    p.Description,
	})
}

func (b *Backend) SetThreadAgentMode(spaceID, parentMessageID, personaID, mode string) error {
	return b.app.Spaces().SetThreadAgentMode(spaceID, parentMessageID, personaID, mode)
}

func normalizeChannelSeed(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
			prevDash = false
		case r == '-' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		case r == ' ':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func (b *Backend) GetChannel(id string) SessionDetail {
	cfg := b.app.Config()
	var sp *space.Space
	if space.IsSpaceID(id) {
		if loaded, err := b.spaceLoader().LoadTyped(id, space.KindChannel); err == nil && loaded != nil {
			sp = loaded
		}
	}
	if sp == nil {
		ensured, err := b.app.Spaces().EnsureSpace(space.KindChannel, "default", space.PersonaInfo{})
		if err != nil {
			return SessionDetail{}
		}
		sp = ensured
	}
	sp = b.recoverPendingMessages(sp)
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        "#" + channelDisplayName(sp, cfg.Workspace),
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
		},
		Summary:  "",
		Messages: spaceMessagesToView(sp, b.app),
	}
}

func spaceMessagesToView(sp *space.Space, a appAccessor) []MessageView {
	if sp == nil {
		return []MessageView{}
	}
	resolver := personaResolver(a)
	var threadInfo map[string]ThreadSummary
	var taskIndex map[string]*taskpkg.Task
	var accessoryIndex map[string]*taskpkg.Task
	if threadKind(sp.Kind) {
		threadInfo, taskIndex = computeThreadInfo(sp, a)
		accessoryIndex = computeTaskAccessoryIndex(sp, a)
	}
	out := make([]MessageView, 0, len(sp.Messages))
	for _, m := range sp.Messages {
		view := baseMessageView(sp, m, resolver)
		if m.ParentMessageID != "" {
			view.IsThreadReply = true
		}
		if info, ok := threadInfo[m.ID]; ok {
			summary := info
			view.ThreadInfo = &summary
		}
		if tk, ok := accessoryIndex[m.ID]; ok && tk != nil {
			view.TaskAccessory = projectTaskAccessory(tk, a)
		}
		_ = taskIndex
		out = append(out, view)
	}
	return out
}

func baseMessageView(sp *space.Space, m space.Message, resolver space.DisplayResolver) MessageView {
	view := MessageView{
		ID:              m.ID,
		Role:            roleForKind(m.AuthorKind),
		AuthorID:        m.AuthorID,
		AuthorName:      space.MessageAuthorDisplay(sp, m, resolver),
		Content:         m.Content,
		Reasoning:       m.Reasoning,
		Status:          m.Status,
		Error:           m.Error,
		Time:            m.CreatedAt,
		ThreadID:        m.ParentMessageID,
		AutoReplyReason: m.AutoReplyReason,
		Mentions:        append([]string(nil), m.Mentions...),
		RuntimeMeta:     copyStringMap(m.RuntimeMeta),
		Attachments:     cloneMessageAttachments(m.Attachments),
	}
	if m.Usage != nil {
		view.Usage = &TokenUsage{
			Input:   m.Usage.Input,
			Output:  m.Usage.Output,
			Total:   m.Usage.Total,
			CostUSD: m.Usage.CostUSD,
			Model:   m.Usage.Model,
			Source:  m.Usage.Source,
		}
	}
	return view
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneMessageAttachments(in []msg.Attachment) []msg.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]msg.Attachment, len(in))
	copy(out, in)
	return out
}

func computeTaskAccessoryIndex(sp *space.Space, a appAccessor) map[string]*taskpkg.Task {
	out := map[string]*taskpkg.Task{}
	if a == nil || a.Tasks() == nil {
		return out
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		return out
	}
	for _, tk := range tasks {
		if tk == nil || strings.TrimSpace(tk.TriggerMessageID) == "" {
			continue
		}
		prev, ok := out[tk.TriggerMessageID]
		if !ok || tk.CreatedAt.After(prev.CreatedAt) {
			out[tk.TriggerMessageID] = tk
		}
	}
	return out
}

func projectTaskAccessory(tk *taskpkg.Task, a appAccessor) *TaskAccessoryInfo {
	if tk == nil {
		return nil
	}
	info := &TaskAccessoryInfo{
		TaskID:        tk.ID,
		WorkerID:      tk.WorkerID,
		WorkerDisplay: resolveWorkerDisplay(tk.WorkerID, a),
		Status:        taskStatusForUI(tk.Status),
	}
	switch tk.Status {
	case taskpkg.StatusFinished, taskpkg.StatusFailed, taskpkg.StatusCanceled, taskpkg.StatusEmptyOutput:
		info.Terminal = true
	}
	if tk.Status == taskpkg.StatusFailed || tk.Status == taskpkg.StatusCanceled {
		info.ShortOutcome = shortOutcome(tk.Outcome)
	}
	return info
}

func shortOutcome(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	rl := []rune(s)
	if len(rl) <= 80 {
		return s
	}
	return string(rl[:80]) + "…"
}

func computeThreadInfo(sp *space.Space, a appAccessor) (map[string]ThreadSummary, map[string]*taskpkg.Task) {
	groups := groupReplies(sp)
	if len(groups) == 0 {
		return nil, nil
	}
	parentIndex := indexMessages(sp.Messages)
	taskIndex := map[string]*taskpkg.Task{}
	if a != nil && a.Tasks() != nil {
		if tasks, err := a.Tasks().ListBySpace(sp.ID); err == nil {
			for _, tk := range tasks {
				if tk.Status.Active() {
					taskIndex[tk.TriggerMessageID] = tk
				}
			}
		}
	}
	out := make(map[string]ThreadSummary, len(groups))
	for parentID, replies := range groups {
		root, ok := parentIndex[parentID]
		if !ok {
			continue
		}
		last := replies[len(replies)-1]
		hasRunning := false
		if _, ok := taskIndex[root.ID]; ok {
			hasRunning = true
		} else {
			for _, r := range replies {
				if _, ok := taskIndex[r.ID]; ok {
					hasRunning = true
					break
				}
			}
		}
		out[parentID] = ThreadSummary{
			ParentID:         root.ID,
			ParentPreview:    preview(root.Content, previewLen),
			ReplyCount:       len(replies),
			LastReplyTime:    last.CreatedAt,
			LastReplyAuthor:  authorDisplay(sp, last, a),
			HasRunningWorker: hasRunning,
		}
	}
	return out, taskIndex
}

func roleForKind(k space.ParticipantKind) string {
	switch k {
	case space.ParticipantUser:
		return "user"
	case space.ParticipantAgent:
		return "agent"
	case space.ParticipantSystem:
		return "system"
	}
	return ""
}

func personaResolver(a appAccessor) space.DisplayResolver {
	if a == nil {
		return nil
	}
	return space.DisplayResolverFunc(func(id string) string {
		return personaDisplay(a, id, "")
	})
}

type appAccessor interface {
	Personas() *persona.Registry
	Tasks() *taskpkg.Manager
}

func timeInWindow(t, lo, hi time.Time) bool {
	if lo.IsZero() && hi.IsZero() {
		return true
	}
	if !lo.IsZero() && t.Before(lo.Add(-2*time.Second)) {
		return false
	}
	if !hi.IsZero() && t.After(hi.Add(2*time.Second)) {
		return false
	}
	return true
}

func dropExploratoryErrors(steps []DelegateStep) []DelegateStep {
	out := steps[:0]
	for _, s := range steps {
		if s.Status == "error" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func humanizeStep(tool, rawArgs, workspace string) string {
	verb := stepVerb(tool)
	target := stepTarget(tool, rawArgs, workspace)
	if target == "" {
		return verb
	}
	return verb + " " + target
}

func stepVerb(tool string) string {
	switch strings.ToLower(tool) {
	case "read":
		return "read"
	case "list_files", "ls":
		return "listed"
	case "bash", "shell", "exec":
		return "ran"
	case "write":
		return "wrote"
	case "edit", "patch":
		return "edited"
	case "grep", "search":
		return "searched"
	default:
		return tool
	}
}

func stepTarget(tool, rawArgs, workspace string) string {
	if rawArgs == "" {
		return ""
	}
	var args struct {
		Path string `json:"path"`
		Cmd  string `json:"cmd"`
		File string `json:"file"`
		Q    string `json:"query"`
		Dir  string `json:"dir"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	switch strings.ToLower(tool) {
	case "read", "write", "edit", "patch":
		if p := strings.TrimSpace(args.Path); p != "" {
			return projectRel(p, workspace)
		}
		if p := strings.TrimSpace(args.File); p != "" {
			return projectRel(p, workspace)
		}
	case "list_files", "ls":
		if p := strings.TrimSpace(args.Path); p != "" {
			return strings.TrimSuffix(projectRel(p, workspace), "/") + "/"
		}
		if p := strings.TrimSpace(args.Dir); p != "" {
			return strings.TrimSuffix(projectRel(p, workspace), "/") + "/"
		}
	case "bash", "shell", "exec":
		if c := strings.TrimSpace(args.Cmd); c != "" {
			return shortCmd(c, workspace)
		}
	case "grep", "search":
		if q := strings.TrimSpace(args.Q); q != "" {
			return "for " + clip(q, 40)
		}
	}
	return ""
}

func projectRel(p, workspace string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if workspace != "" && strings.HasPrefix(p, workspace+"/") {
		rel := strings.TrimPrefix(p, workspace+"/")
		return clip(rel, 60)
	}
	if workspace != "" && p == workspace {
		return "./"
	}
	if !strings.HasPrefix(p, "/") {
		return clip(p, 60)
	}
	return basenameOf(p)
}

func basenameOf(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func shortCmd(c, workspace string) string {
	c = strings.TrimSpace(c)
	if workspace != "" {
		c = strings.ReplaceAll(c, workspace+"/", "")
		c = strings.ReplaceAll(c, workspace, ".")
	}
	if i := strings.IndexAny(c, " \t"); i > 0 {
		head := c[:i]
		rest := strings.TrimSpace(c[i:])
		if rest == "" {
			return head
		}
		return clip(head+" "+rest, 60)
	}
	return clip(c, 60)
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 120 {
		r := []rune(s)
		s = string(r[:120]) + "…"
	}
	return s
}

func parseTaskID(s string) string {
	const tag = "task_id="
	i := strings.Index(s, tag)
	if i < 0 {
		return ""
	}
	rest := s[i+len(tag):]
	end := strings.IndexAny(rest, " \n\t,)")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func questionFromArgs(rawArgs string) string {
	if rawArgs == "" {
		return ""
	}
	var args struct {
		Question string `json:"question"`
		Task     string `json:"task"`
		Prompt   string `json:"prompt"`
		Input    string `json:"input"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	for _, v := range []string{args.Question, args.Task, args.Prompt, args.Input} {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (b *Backend) GetParticipants(channelID, threadID string) ParticipantsView {
	spaceID := strings.TrimSpace(threadID)
	if spaceID == "" {
		spaceID = strings.TrimSpace(channelID)
	}
	if spaceID == "" {
		return ParticipantsView{Agents: b.allAgents()}
	}
	sp, err := b.app.Spaces().LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ParticipantsView{Agents: []AgentItem{}}
	}
	recentRuns, archivedRuns := b.spaceRecentRuns(sp)
	return ParticipantsView{
		Agents:            spaceParticipantsAsAgents(sp, b.app),
		ActiveRuns:        b.pendingActiveRuns(sp.ID, ""),
		RecentRuns:        recentRuns,
		ArchivedRunsCount: archivedRuns,
	}
}

func (b *Backend) pendingActiveRuns(spaceID, parentMessageID string) []AgentRun {
	if b == nil {
		return nil
	}
	spaceID = strings.TrimSpace(spaceID)
	parentMessageID = strings.TrimSpace(parentMessageID)
	b.mu.Lock()
	pending := make([]pendingTurn, 0, len(b.pending))
	for _, p := range b.pending {
		if strings.TrimSpace(p.SpaceID) != spaceID {
			continue
		}
		if strings.TrimSpace(p.ParentMessageID) != parentMessageID {
			continue
		}
		pending = append(pending, p)
	}
	b.mu.Unlock()
	out := make([]AgentRun, 0, len(pending))
	for _, p := range pending {
		started := p.StartedAt
		if started.IsZero() {
			started = time.Now()
		}
		out = append(out, AgentRun{
			ID:              p.StreamID,
			AgentID:         p.AgentID,
			Title:           "Working now",
			Status:          "running",
			Lifecycle:       "active",
			SpaceID:         p.SpaceID,
			ParentMessageID: p.ParentMessageID,
			Time:            started,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

func spaceParticipantsAsAgents(sp *space.Space, a appAccessor) []AgentItem {
	if sp == nil {
		return []AgentItem{}
	}
	out := make([]AgentItem, 0, len(sp.Participants))
	for _, p := range sp.Participants {
		if p.Kind != space.ParticipantAgent {
			continue
		}
		display := p.Display
		role := p.Role
		if a != nil {
			if pp := a.Personas().Get(p.ID); pp != nil {
				if display == "" {
					display = pp.Display
				}
				if role == "" {
					role = pp.Description
				}
			}
		}
		if display == "" {
			display = p.ID
		}
		out = append(out, AgentItem{
			ID:      p.ID,
			Display: display,
			Role:    role,
			Runtime: personaRuntime(p.ID, a),
			Model:   personaModel(p.ID, a),
			Status:  "idle",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *Backend) spaceRecentRuns(sp *space.Space) ([]AgentRun, int) {
	if sp == nil || b.app.Tasks() == nil {
		return nil, 0
	}
	tasks, err := b.app.Tasks().ListBySpace(sp.ID)
	if err != nil {
		return nil, 0
	}
	out := make([]AgentRun, 0, len(tasks))
	archived := 0
	for _, tk := range tasks {
		if tk.Status.Active() {
			out = append(out, agentRunFromTask(tk, sp))
		} else {
			archived++
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, archived
}

func agentRunFromTask(tk *taskpkg.Task, sp *space.Space) AgentRun {
	parentID := taskParentMessageID(tk, sp)
	return AgentRun{
		ID:                 tk.ID,
		AgentID:            tk.WorkerID,
		Title:              tk.Title,
		Status:             taskStatusForUI(tk.Status),
		Lifecycle:          string(tk.Status.Lifecycle()),
		CreatedBy:          taskCreatedBy(tk),
		AssignedBy:         tk.AssignedBy,
		SpaceID:            tk.SpaceID,
		ThreadID:           tk.SourceThreadID,
		TriggerMessageID:   tk.TriggerMessageID,
		ParentMessageID:    parentID,
		ExpectedOutcome:    tk.ExpectedOutcome,
		AcceptanceCriteria: tk.AcceptanceCriteria,
		Time:               tk.UpdatedAt,
	}
}

func taskParentMessageID(tk *taskpkg.Task, sp *space.Space) string {
	if tk == nil || sp == nil || strings.TrimSpace(tk.TriggerMessageID) == "" {
		return ""
	}
	for _, m := range sp.Messages {
		if m.ID == tk.TriggerMessageID {
			if strings.TrimSpace(m.ParentMessageID) != "" {
				return m.ParentMessageID
			}
			if sp.Kind == space.KindChannel || sp.Kind == space.KindDirectChat {
				return m.ID
			}
			return ""
		}
	}
	return ""
}

func taskStatusForUI(s taskpkg.Status) string {
	switch s {
	case taskpkg.StatusEmptyOutput:
		return "no_output"
	default:
		return string(s)
	}
}

type RunStep struct {
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
}

type RunDetail struct {
	TaskID             string        `json:"task_id"`
	SpaceID            string        `json:"space_id"`
	WorkerID           string        `json:"worker_id"`
	WorkerName         string        `json:"worker_name,omitempty"`
	CreatedBy          string        `json:"created_by,omitempty"`
	AssignedBy         string        `json:"assigned_by,omitempty"`
	Title              string        `json:"title"`
	Status             string        `json:"status"`
	ExpectedOutcome    string        `json:"expected_outcome,omitempty"`
	AcceptanceCriteria string        `json:"acceptance_criteria,omitempty"`
	Outcome            string        `json:"outcome,omitempty"`
	ResultMessageID    string        `json:"result_message_id,omitempty"`
	TriggerMessageID   string        `json:"trigger_message_id,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	KeySteps           []RunStep     `json:"key_steps,omitempty"`
	State              TaskStateView `json:"state,omitempty"`
}

func (b *Backend) GetRunDetail(taskID string) RunDetail {
	if b.app.Tasks() == nil {
		return RunDetail{}
	}
	tk, err := b.app.Tasks().Get(strings.TrimSpace(taskID))
	if err != nil || tk == nil {
		return RunDetail{}
	}
	detail := RunDetail{
		TaskID:             tk.ID,
		SpaceID:            tk.SpaceID,
		WorkerID:           tk.WorkerID,
		WorkerName:         resolveWorkerDisplay(tk.WorkerID, b.app),
		CreatedBy:          taskCreatedBy(tk),
		AssignedBy:         tk.AssignedBy,
		Title:              tk.Title,
		Status:             taskStatusForUI(tk.Status),
		ExpectedOutcome:    tk.ExpectedOutcome,
		AcceptanceCriteria: tk.AcceptanceCriteria,
		Outcome:            tk.Outcome,
		ResultMessageID:    tk.ResultMessageID,
		TriggerMessageID:   tk.TriggerMessageID,
		CreatedAt:          tk.CreatedAt,
		UpdatedAt:          tk.UpdatedAt,
		State:              taskStateView(tk.State),
	}
	runs, err := b.app.Tasks().ListRuns(tk.ID)
	if err != nil {
		return detail
	}
	if len(runs) == 0 {
		return detail
	}
	latest := runs[0]
	for _, r := range runs[1:] {
		if r.StartedAt.After(latest.StartedAt) {
			latest = r
		}
	}
	steps := make([]RunStep, 0, len(latest.KeySteps))
	for _, s := range latest.KeySteps {
		steps = append(steps, RunStep{
			Kind:  string(s.Kind),
			Title: s.Title,
			At:    s.At,
			OK:    s.OK,
		})
	}
	detail.KeySteps = steps
	if detail.State.Goal == "" && detail.State.Checkpoint == "" && len(detail.State.Todo) == 0 {
		detail.State = taskStateView(latest.State)
	}
	return detail
}

func resolveWorkerDisplay(workerID string, a appAccessor) string {
	if a == nil || strings.TrimSpace(workerID) == "" {
		return ""
	}
	return personaDisplay(a, workerID, workerID)
}

func personaDisplay(a appAccessor, id, fallback string) string {
	if a == nil || strings.TrimSpace(id) == "" {
		return fallback
	}
	if p := a.Personas().Get(id); p != nil && strings.TrimSpace(p.Display) != "" {
		return p.Display
	}
	return fallback
}

func personaAgentItem(p *persona.Persona, status string) AgentItem {
	if p == nil {
		return AgentItem{Status: status}
	}
	return AgentItem{
		ID:      p.ID,
		Display: p.Display,
		Role:    p.Description,
		Runtime: p.Runtime,
		Model:   p.Model,
		Status:  status,
	}
}

func personaRuntime(id string, a appAccessor) string {
	if a == nil || strings.TrimSpace(id) == "" {
		return ""
	}
	if p := a.Personas().Get(id); p != nil {
		return p.Runtime
	}
	return ""
}

func personaModel(id string, a appAccessor) string {
	if a == nil || strings.TrimSpace(id) == "" {
		return ""
	}
	if p := a.Personas().Get(id); p != nil {
		return p.Model
	}
	return ""
}

func (b *Backend) allAgents() []AgentItem {
	out := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, personaAgentItem(p, "idle"))
	}
	return out
}

func (b *Backend) GetAgentDM(agentID string) SessionDetail {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return SessionDetail{}
	}
	var sp *space.Space
	if space.IsSpaceID(agentID) {
		if loaded, err := b.spaceLoader().LoadTyped(agentID, space.KindAgentDM); err == nil && loaded != nil {
			sp = loaded
		}
		if sp == nil {
			return SessionDetail{}
		}
	}
	display := agentID
	role := ""
	if sp != nil {
		if pid := space.AgentParticipantID(sp); pid != "" {
			if p := b.app.Personas().Get(pid); p != nil {
				display = p.Display
				role = p.Description
			} else {
				display = pid
			}
		}
	} else {
		p := b.app.Personas().Get(agentID)
		if p == nil {
			return SessionDetail{}
		}
		display = p.Display
		role = p.Description
		ensured, err := b.app.Spaces().EnsureSpace(space.KindAgentDM, agentID, space.PersonaInfo{
			ID:      agentID,
			Display: display,
			Role:    role,
		})
		if err != nil {
			return SessionDetail{}
		}
		sp = ensured
	}
	sp = b.recoverPendingMessages(sp)
	pid := space.AgentParticipantID(sp)
	if pid == "" {
		pid = strings.TrimSpace(agentID)
	}
	title := visibleAgentDMTitle(sp, pid)
	if title == "New chat" {
		title = "@" + display
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        title,
			PersonaID:    pid,
			PersonaName:  display,
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
			Running: b.hasActiveTurn(
				sp.ID,
				"desktop:agent:"+sp.ID,
				"desktop:agent:"+pid,
			),
		},
		ActiveRuns: b.pendingActiveRuns(sp.ID, ""),
		Messages:   spaceMessagesToView(sp, b.app),
	}
}

func (b *Backend) CreateAgentDM(personaID, title string) (AgentDMItem, error) {
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return AgentDMItem{}, fmt.Errorf("persona id required")
	}
	p := b.app.Personas().Get(personaID)
	if p == nil {
		return AgentDMItem{}, fmt.Errorf("persona not registered: %s", personaID)
	}
	if title = strings.TrimSpace(title); title == "" {
		if sp, err := b.findDefaultAgentDM(personaID); err == nil && sp != nil {
			return agentDMItemFromSpace(sp, b.app), nil
		}
	}
	seed := p.ID + "-" + uuid.NewString()[:8]
	info := space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
	sp, err := b.app.Spaces().EnsureSpace(space.KindAgentDM, seed, info)
	if err != nil {
		return AgentDMItem{}, err
	}
	if title = strings.TrimSpace(title); title != "" {
		if err := b.app.Spaces().UpdateTitle(sp.ID, title); err != nil {
			return AgentDMItem{}, err
		}
		if updated, err := b.app.Spaces().LoadSpace(sp.ID); err == nil && updated != nil {
			sp = updated
		}
	}
	return agentDMItemFromSpace(sp, b.app), nil
}

func (b *Backend) findDefaultAgentDM(personaID string) (*space.Space, error) {
	all, err := b.app.Spaces().Store().ListSpaces()
	if err != nil {
		return nil, err
	}
	for _, sp := range all {
		if sp.Kind != space.KindAgentDM || !isDefaultAgentDM(sp) {
			continue
		}
		if space.AgentParticipantID(sp) == personaID {
			return sp, nil
		}
	}
	return nil, nil
}

func (b *Backend) UpdateAgentDMTitle(spaceID, title string) (AgentDMItem, error) {
	spaceID = strings.TrimSpace(spaceID)
	title = strings.TrimSpace(title)
	if spaceID == "" || title == "" {
		return AgentDMItem{}, fmt.Errorf("space id and title required")
	}
	sp, err := b.spaceLoader().LoadTyped(spaceID, space.KindAgentDM)
	if err != nil {
		return AgentDMItem{}, err
	}
	if sp == nil {
		return AgentDMItem{}, fmt.Errorf("agent chat not found: %s", spaceID)
	}
	if isDefaultAgentDM(sp) {
		return AgentDMItem{}, fmt.Errorf("default agent dm title is fixed")
	}
	if err := b.app.Spaces().UpdateTitle(sp.ID, title); err != nil {
		return AgentDMItem{}, err
	}
	updated, err := b.app.Spaces().LoadSpace(sp.ID)
	if err != nil {
		return AgentDMItem{}, err
	}
	return agentDMItemFromSpace(updated, b.app), nil
}

func (b *Backend) ListAgentDMs() []AgentDMItem {
	all, err := b.app.Spaces().Store().ListSpaces()
	if err != nil {
		return []AgentDMItem{}
	}
	out := make([]AgentDMItem, 0, len(all))
	for _, sp := range all {
		if sp.Kind != space.KindAgentDM {
			continue
		}
		if isDefaultAgentDM(sp) {
			continue
		}
		if b.app.Personas().Get(space.AgentParticipantID(sp)) == nil {
			continue
		}
		out = append(out, agentDMItemFromSpace(sp, b.app))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func agentDMItemFromSpace(sp *space.Space, a appAccessor) AgentDMItem {
	pid := space.AgentParticipantID(sp)
	display := pid
	if a != nil {
		if p := a.Personas().Get(pid); p != nil && strings.TrimSpace(p.Display) != "" {
			display = p.Display
		}
	}
	title := visibleAgentDMTitle(sp, pid)
	return AgentDMItem{
		ID:           sp.ID,
		PersonaID:    pid,
		PersonaName:  display,
		Title:        title,
		UpdatedAt:    sp.UpdatedAt,
		MessageCount: len(sp.Messages),
	}
}

func visibleAgentDMTitle(sp *space.Space, personaID string) string {
	if sp == nil {
		return "New chat"
	}
	t := strings.TrimSpace(sp.Title)
	if t == strings.TrimSpace(personaID) {
		return "New chat"
	}
	if t == "" || isAgentDMMachineSeed(t, personaID) {
		return "New chat"
	}
	return t
}

func isDefaultAgentDM(sp *space.Space) bool {
	if sp == nil || sp.Kind != space.KindAgentDM {
		return false
	}
	pid := space.AgentParticipantID(sp)
	return pid != "" && strings.TrimSpace(sp.Title) == pid
}

func isAgentDMMachineSeed(t, personaID string) bool {
	if personaID == "" || !strings.HasPrefix(t, personaID+"-") {
		return false
	}
	tail := t[len(personaID)+1:]
	if len(tail) != 8 {
		return false
	}
	for _, r := range tail {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (b *Backend) ListPersonas() []PersonaItem {
	out := make([]PersonaItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, PersonaItem{
			ID:            p.ID,
			Display:       p.Display,
			Runtime:       p.Runtime,
			Model:         p.Model,
			Description:   p.Description,
			Tools:         p.Tools,
			Capabilities:  p.Capabilities,
			TaskPolicy:    p.TaskPolicy,
			ShowInSidebar: p.ShowInSidebar,
		})
	}
	return out
}

func (b *Backend) ListModels() []ModelItem {
	cfg := b.app.Config()
	out := make([]ModelItem, 0, len(cfg.Models))
	for name, m := range cfg.Models {
		out = append(out, ModelItem{
			Name:          name,
			Provider:      m.Provider,
			Model:         m.Model,
			MaxTokens:     m.MaxTokens,
			ContextWindow: m.ContextWindow,
			Ready:         m.APIKey != "",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (b *Backend) ListTools() []ToolItem {
	tools := b.app.Tools()
	out := make([]ToolItem, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolItem{
			Name:        t.Name(),
			Description: t.Desc(),
			Enabled:     true,
		})
	}
	return out
}

func (b *Backend) ListCommands() []CommandItem {
	cmds := b.app.Commands()
	out := make([]CommandItem, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, CommandItem{Name: "/" + c.Name(), Summary: c.Desc()})
	}
	return out
}

func (b *Backend) UpdateTaskStatus(taskID, status string) (AgentRun, error) {
	if b.app.Tasks() == nil {
		return AgentRun{}, fmt.Errorf("tasks not available")
	}
	next, err := kanbanTaskStatus(status)
	if err != nil {
		return AgentRun{}, err
	}
	tk, err := b.app.Tasks().Update(strings.TrimSpace(taskID), taskpkg.UpdateTaskInput{Status: next})
	if err != nil {
		return AgentRun{}, err
	}
	var sp *space.Space
	if b.app.Spaces() != nil {
		sp, _ = b.app.Spaces().LoadSpace(tk.SpaceID)
	}
	return agentRunFromTask(tk, sp), nil
}

type CreateTaskRequest struct {
	SpaceID            string `json:"space_id"`
	SourceMessage      string `json:"source_message"`
	SourceMessageID    string `json:"source_message_id"`
	SourceThread       string `json:"source_thread"`
	SourceThreadID     string `json:"source_thread_id"`
	CreatedBy          string `json:"created_by"`
	AssigneeID         string `json:"assignee_id"`
	Assignee           string `json:"assignee"`
	AssignedBy         string `json:"assigned_by"`
	Title              string `json:"title"`
	Outcome            string `json:"outcome"`
	ExpectedOutcome    string `json:"expected_outcome"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Source             string `json:"source"`
	ExplicitTaskIntent bool   `json:"explicit_task_intent"`
}

type AssignTaskRequest struct {
	TaskID             string `json:"task_id"`
	AssigneeID         string `json:"assignee_id"`
	Assignee           string `json:"assignee"`
	AssignedBy         string `json:"assigned_by"`
	Outcome            string `json:"outcome"`
	ExpectedOutcome    string `json:"expected_outcome"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
}

func (b *Backend) CreateTask(in CreateTaskRequest) (TaskStateCard, error) {
	if b.app.Tasks() == nil {
		return TaskStateCard{}, fmt.Errorf("tasks not available")
	}
	assignee := firstNonEmpty(in.AssigneeID, in.Assignee)
	createdBy := firstNonEmpty(in.CreatedBy, b.defaultActorID())
	assignedBy := firstNonEmpty(in.AssignedBy, createdBy)
	expected := firstNonEmpty(in.ExpectedOutcome, in.Outcome)
	if !in.ExplicitTaskIntent {
		return TaskStateCard{}, fmt.Errorf("task creation requires explicit user task intent")
	}
	if err := validateTaskCommitment(in.Title, expected, in.AcceptanceCriteria); err != nil {
		return TaskStateCard{}, err
	}
	sourceMessageID := firstNonEmpty(in.SourceMessageID, in.SourceMessage)
	sourceThreadID := firstNonEmpty(in.SourceThreadID, in.SourceThread)
	tk, err := b.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:            in.SpaceID,
		TriggerMessageID:   sourceMessageID,
		SourceThreadID:     sourceThreadID,
		InitiatorID:        createdBy,
		CreatedBy:          createdBy,
		WorkerID:           assignee,
		AssignedBy:         assignedBy,
		Title:              in.Title,
		ExpectedOutcome:    expected,
		AcceptanceCriteria: in.AcceptanceCriteria,
		Source:             firstNonEmpty(in.Source, desktopSource),
	})
	if err != nil {
		return TaskStateCard{}, err
	}
	return b.taskCardFromTask(tk), nil
}

func validateTaskCommitment(title, expected, criteria string) error {
	title = strings.TrimSpace(title)
	expected = strings.TrimSpace(expected)
	criteria = strings.TrimSpace(criteria)
	if title == "" || expected == "" || criteria == "" {
		return fmt.Errorf("task requires title, expected_outcome, and acceptance_criteria")
	}
	if vagueTaskField(title) || vagueTaskField(expected) || vagueTaskField(criteria) {
		return fmt.Errorf("task requires a concrete deliverable with reviewable acceptance criteria")
	}
	if len([]rune(expected)) < 12 || len([]rune(criteria)) < 12 {
		return fmt.Errorf("task expected_outcome and acceptance_criteria must describe a reviewable deliverable")
	}
	return nil
}

func vagueTaskField(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return true
	}
	s = strings.Join(strings.Fields(s), " ")
	switch s {
	case "done", "finish", "finished", "complete", "completed", "ok", "fixed", "reviewed",
		"处理", "处理完", "完成", "搞定", "看一下", "查一下", "解释一下", "看看":
		return true
	default:
		return false
	}
}

func (b *Backend) AssignTask(in AssignTaskRequest) (TaskStateCard, error) {
	if b.app.Tasks() == nil {
		return TaskStateCard{}, fmt.Errorf("tasks not available")
	}
	assignee := firstNonEmpty(in.AssigneeID, in.Assignee)
	assignedBy := firstNonEmpty(in.AssignedBy, b.defaultActorID())
	expected := firstNonEmpty(in.ExpectedOutcome, in.Outcome)
	tk, err := b.app.Tasks().Update(strings.TrimSpace(in.TaskID), taskpkg.UpdateTaskInput{
		WorkerID:           assignee,
		AssignedBy:         assignedBy,
		ExpectedOutcome:    expected,
		AcceptanceCriteria: in.AcceptanceCriteria,
	})
	if err != nil {
		return TaskStateCard{}, err
	}
	return b.taskCardFromTask(tk), nil
}

func (b *Backend) taskCardFromTask(tk *taskpkg.Task) TaskStateCard {
	if tk == nil {
		return TaskStateCard{}
	}
	parentID := ""
	if b.app.Spaces() != nil && tk != nil {
		if sp, err := b.app.Spaces().LoadSpace(tk.SpaceID); err == nil {
			parentID = taskParentMessageID(tk, sp)
		}
	}
	return taskStateCard(app.TaskStateSummary{
		ID:                 tk.ID,
		Title:              tk.Title,
		Status:             string(tk.Status),
		Lifecycle:          string(tk.Status.Lifecycle()),
		CreatedBy:          taskCreatedBy(tk),
		WorkerID:           tk.WorkerID,
		AssigneeID:         tk.WorkerID,
		Assignee:           tk.WorkerID,
		AssignedBy:         tk.AssignedBy,
		SpaceID:            tk.SpaceID,
		Source:             tk.Source,
		SourceMessageID:    tk.TriggerMessageID,
		SourceThreadID:     tk.SourceThreadID,
		SourceThread:       tk.SourceThreadID,
		TriggerMessageID:   tk.TriggerMessageID,
		ParentMessageID:    parentID,
		UpdatedAt:          tk.UpdatedAt,
		ExpectedOutcome:    tk.ExpectedOutcome,
		AcceptanceCriteria: tk.AcceptanceCriteria,
		Outcome:            tk.Outcome,
		State:              tk.State,
	})
}

func (b *Backend) defaultActorID() string {
	if b.app != nil && b.app.Spaces() != nil {
		if id := strings.TrimSpace(b.app.Spaces().UserParticipant().ID); id != "" {
			return id
		}
	}
	return "user"
}

func kanbanTaskStatus(status string) (taskpkg.Status, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "todo", "queued":
		return taskpkg.StatusQueued, nil
	case "doing", "running", "in_progress":
		return taskpkg.StatusRunning, nil
	case "review", "in_review", "in-review":
		return taskpkg.Status("in_review"), nil
	case "done", "finished":
		return taskpkg.StatusFinished, nil
	case "closed", "close", "canceled", "cancelled":
		return taskpkg.StatusCanceled, nil
	default:
		return "", fmt.Errorf("unsupported task status: %s", status)
	}
}

func (b *Backend) Capabilities() CapabilityView {
	return CapabilityView{
		Skills:                 skillViews(b.app.SkillDirectory()),
		Tasks:                  taskStateCards(b.app.RecentTaskStates(50)),
		ArchivedTaskStateCount: b.app.ArchivedTaskStateCount(),
		ActionProposals:        actionProposalCards(b.app.RecentActionProposals(6)),
	}
}

func (b *Backend) MemoryOverview(personaID, source, spaceID string) app.MemoryOverview {
	personaID = strings.TrimSpace(personaID)
	source = strings.TrimSpace(source)
	spaceID = strings.TrimSpace(spaceID)
	if spaceID != "" {
		if sp, err := b.app.Spaces().LoadSpace(spaceID); err == nil && sp != nil {
			if source == "" {
				source = contextSourceForSpace(sp)
			}
			if personaID == "" && sp.Kind == space.KindAgentDM {
				personaID = space.AgentParticipantID(sp)
			}
		}
	}
	scopes := make([]command.MemoryScope, 0, 4)
	if personaID != "" {
		scopes = append(scopes, command.MemoryScope{Kind: "persona", Key: personaID})
	}
	if source != "" {
		scopes = append(scopes, command.MemoryScope{Kind: "channel", Key: source})
	}
	if workspace := strings.TrimSpace(b.app.Workspace()); workspace != "" {
		scopes = append(scopes, command.MemoryScope{Kind: "workspace", Key: workspace})
	}
	scopes = append(scopes, command.MemoryScope{Kind: "global", Key: ""})
	return b.app.MemoryOverview(scopes, 3)
}

func (b *Backend) DeleteMemory(req DeleteMemoryRequest) (DeleteMemoryResult, error) {
	scopeToken := strings.TrimSpace(req.Scope)
	memoryID := strings.TrimSpace(req.ID)
	if scopeToken == "" || memoryID == "" {
		return DeleteMemoryResult{}, fmt.Errorf("memory scope and id required")
	}
	spaceID := strings.TrimSpace(req.SpaceID)
	personaID := strings.TrimSpace(req.PersonaID)
	source := strings.TrimSpace(req.Source)
	if spaceID != "" {
		if sp, err := b.app.Spaces().LoadSpace(spaceID); err == nil && sp != nil {
			if source == "" {
				source = contextSourceForSpace(sp)
			}
			if personaID == "" && sp.Kind == space.KindAgentDM {
				personaID = space.AgentParticipantID(sp)
			}
		}
	}
	ctx := context.Background()
	if source != "" {
		ctx = command.WithSource(ctx, source)
	}
	input := "!memory delete " + scopeToken + " " + memoryID
	out, err := b.app.HandleInput(ctx, firstNonEmpty(source, desktopSource), input)
	if err != nil {
		if spaceID != "" {
			_, _, _ = b.app.Spaces().AppendMessageWithRouting(spaceID, space.Message{
				AuthorID:   "sumi",
				AuthorKind: space.ParticipantSystem,
				Content:    "Memory delete failed: " + err.Error(),
			}, nil, nil)
		}
		return DeleteMemoryResult{}, err
	}
	if spaceID != "" {
		b.persistCommandOutputForSpace(spaceID, "", input, out)
	}
	return DeleteMemoryResult{
		OK:       true,
		Output:   strings.TrimSpace(out),
		Overview: b.MemoryOverview(personaID, source, spaceID),
	}, nil
}

func (b *Backend) GetMemory(req GetMemoryRequest) (app.MemoryDocDetail, error) {
	scopeKind, scopeKey := parseDesktopMemoryScope(req.Scope)
	return b.app.MemoryDoc(scopeKind, scopeKey, req.ID)
}

func (b *Backend) UpdateMemory(req UpdateMemoryRequest) (UpdateMemoryResult, error) {
	scopeKind, scopeKey := parseDesktopMemoryScope(req.Scope)
	detail, err := b.app.UpdateMemoryDoc(app.MemoryUpdateInput{
		ScopeKind:  scopeKind,
		ScopeKey:   scopeKey,
		ID:         strings.TrimSpace(req.ID),
		Title:      req.Title,
		Body:       req.Body,
		Summary:    req.Summary,
		Kind:       req.Kind,
		Confidence: req.Confidence,
	})
	if err != nil {
		if spaceID := strings.TrimSpace(req.SpaceID); spaceID != "" {
			_, _, _ = b.app.Spaces().AppendMessageWithRouting(spaceID, space.Message{
				AuthorID:   "sumi",
				AuthorKind: space.ParticipantSystem,
				Content:    "Memory update failed: " + err.Error(),
			}, nil, nil)
		}
		return UpdateMemoryResult{}, err
	}
	output := fmt.Sprintf("updated memory %s in %s", detail.ID, detail.ScopeLabel)
	if spaceID := strings.TrimSpace(req.SpaceID); spaceID != "" {
		_, _, _ = b.app.Spaces().AppendMessageWithRouting(spaceID, space.Message{
			AuthorID:   "sumi",
			AuthorKind: space.ParticipantSystem,
			Content:    output,
		}, nil, nil)
	}
	personaID := strings.TrimSpace(req.PersonaID)
	source := strings.TrimSpace(req.Source)
	spaceID := strings.TrimSpace(req.SpaceID)
	if spaceID != "" {
		if sp, err := b.app.Spaces().LoadSpace(spaceID); err == nil && sp != nil {
			if source == "" {
				source = contextSourceForSpace(sp)
			}
			if personaID == "" && sp.Kind == space.KindAgentDM {
				personaID = space.AgentParticipantID(sp)
			}
		}
	}
	return UpdateMemoryResult{
		OK:       true,
		Output:   output,
		Memory:   detail,
		Overview: b.MemoryOverview(personaID, source, spaceID),
	}, nil
}

func parseDesktopMemoryScope(label string) (string, string) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", ""
	}
	if label == "workspace" {
		return "workspace", ""
	}
	if !strings.Contains(label, ":") {
		return label, ""
	}
	kind, key, _ := strings.Cut(label, ":")
	return strings.TrimSpace(kind), strings.TrimSpace(key)
}

func (b *Backend) ListSkills() []SkillView {
	return skillViews(b.app.SkillDirectory())
}

func (b *Backend) GetSkill(name string) SkillView {
	item, ok := b.app.SkillDetail(name)
	if !ok {
		return SkillView{}
	}
	return skillView(item)
}

func skillViews(in []app.SkillDirectoryItem) []SkillView {
	out := make([]SkillView, 0, len(in))
	for _, s := range in {
		out = append(out, skillView(s))
	}
	return out
}

func skillView(s app.SkillDirectoryItem) SkillView {
	return SkillView{
		Name:          s.Name,
		Description:   s.Description,
		When:          s.When,
		Risk:          s.Risk,
		Env:           s.Env,
		EnvNeeds:      skillEnvNeedViews(s.EnvNeeds),
		Entrypoints:   s.Entrypoints,
		Examples:      s.Examples,
		Path:          s.Path,
		Configured:    s.Configured,
		MissingEnv:    s.MissingEnv,
		LastAction:    s.LastAction,
		LastListed:    s.LastListed,
		LastDescribed: s.LastDescribed,
		LastUsed:      s.LastUsed,
		Body:          s.Body,
	}
}

func skillEnvNeedViews(in []app.SkillEnvNeed) []SkillEnvNeed {
	out := make([]SkillEnvNeed, 0, len(in))
	for _, need := range in {
		out = append(out, SkillEnvNeed{
			Name:       need.Name,
			Configured: need.Configured,
			Hint:       need.Hint,
		})
	}
	return out
}

func taskStateCards(in []app.TaskStateSummary) []TaskStateCard {
	out := make([]TaskStateCard, 0, len(in))
	for _, t := range in {
		out = append(out, taskStateCard(t))
	}
	return out
}

func taskStateCard(t app.TaskStateSummary) TaskStateCard {
	return TaskStateCard{
		ID:                 t.ID,
		Title:              t.Title,
		Status:             t.Status,
		Lifecycle:          t.Lifecycle,
		CreatedBy:          t.CreatedBy,
		WorkerID:           t.WorkerID,
		AssigneeID:         firstNonEmpty(t.AssigneeID, t.WorkerID),
		Assignee:           firstNonEmpty(t.Assignee, t.AssigneeID, t.WorkerID),
		AssignedBy:         t.AssignedBy,
		SpaceID:            t.SpaceID,
		Source:             t.Source,
		SourceMessageID:    t.SourceMessageID,
		SourceThreadID:     t.SourceThreadID,
		SourceThread:       t.SourceThread,
		TriggerMessageID:   t.TriggerMessageID,
		ParentMessageID:    t.ParentMessageID,
		UpdatedAt:          t.UpdatedAt,
		ExpectedOutcome:    t.ExpectedOutcome,
		AcceptanceCriteria: t.AcceptanceCriteria,
		Outcome:            t.Outcome,
		State:              taskStateView(t.State),
		LatestRun:          t.LatestRun,
		RunStatus:          t.RunStatus,
		RunStarted:         t.RunStarted,
	}
}

func actionProposalCards(in []app.ActionProposalSummary) []ActionProposalCard {
	out := make([]ActionProposalCard, 0, len(in))
	for _, p := range in {
		out = append(out, ActionProposalCard{
			Time:      p.Time,
			Source:    p.Source,
			Tool:      p.Tool,
			Result:    p.Result,
			Intent:    p.Proposal.Intent,
			Target:    p.Proposal.Target,
			Risk:      p.Proposal.Risk,
			Preview:   p.Proposal.Preview,
			Rollback:  p.Proposal.Rollback,
			ExpiresAt: p.Proposal.ExpiresAt,
		})
	}
	return out
}

func taskStateView(s taskpkg.TaskState) TaskStateView {
	return TaskStateView{
		Goal:       s.Goal,
		Todo:       s.Todo,
		Checkpoint: s.Checkpoint,
		Artifacts:  s.Artifacts,
		Blockers:   s.Blockers,
		RelatedIDs: s.RelatedIDs,
	}
}

func (b *Backend) Subscribe() (<-chan BusEvent, func()) {
	return b.subs.subscribe(256)
}

func (b *Backend) APIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", jsonHandler(func() any { return b.WorkspaceInfo() }))
	mux.HandleFunc("/api/conversation/delete", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in DeleteConversationRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.DeleteConversation(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/context/inspect", func(rw http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		out, err := b.InspectContext(ContextInspectRequest{
			SpaceID:         q.Get("space_id"),
			Source:          q.Get("source"),
			SessionSource:   q.Get("session_source"),
			ParentMessageID: q.Get("parent_message_id"),
			AgentID:         q.Get("agent_id"),
			Profile:         q.Get("profile"),
		})
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/context/reset", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in ContextResetRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.ResetContext(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/channels", jsonHandler(func() any { return b.ListChannels() }))
	mux.HandleFunc("/api/channel/create", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.CreateChannel(in.Name)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("/api/channel/agent-mode", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ChannelID string `json:"channel_id"`
			PersonaID string `json:"persona_id"`
			Mode      string `json:"mode"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.SetChannelAgentMode(in.ChannelID, in.PersonaID, in.Mode); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/channel/add-agent", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ChannelID string `json:"channel_id"`
			PersonaID string `json:"persona_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.AddAgentToChannel(in.ChannelID, in.PersonaID); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/thread/agent-mode", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			SpaceID         string `json:"space_id"`
			ParentMessageID string `json:"parent_message_id"`
			PersonaID       string `json:"persona_id"`
			Mode            string `json:"mode"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.SetThreadAgentMode(in.SpaceID, in.ParentMessageID, in.PersonaID, in.Mode); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/threads", jsonHandler(func() any { return b.ListThreads() }))
	mux.HandleFunc("/api/agents", jsonHandler(func() any { return b.ListAgents() }))
	mux.HandleFunc("/api/channel", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetChannel(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/participants", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetParticipants(req.URL.Query().Get("channel"), req.URL.Query().Get("thread")))
	})
	mux.HandleFunc("/api/agent-dm", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetAgentDM(req.URL.Query().Get("agent")))
	})
	mux.HandleFunc("/api/agent-dms", jsonHandler(func() any { return b.ListAgentDMs() }))
	mux.HandleFunc("/api/agent-dm/create", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			PersonaID string `json:"persona_id"`
			Title     string `json:"title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.CreateAgentDM(in.PersonaID, in.Title)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("/api/agent-dm/title", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.UpdateAgentDMTitle(in.ID, in.Title)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("/api/direct-chats", jsonHandler(func() any { return b.ListDirectChats() }))
	mux.HandleFunc("/api/direct-chat", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetDirectChat(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/direct-chat/title", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.UpdateDirectChatTitle(in.ID, in.Title)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("/api/recent", jsonHandler(func() any { return b.ListRecent() }))
	mux.HandleFunc("/api/run", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetRunDetail(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/task/status", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.UpdateTaskStatus(in.TaskID, in.Status)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/task/create", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in CreateTaskRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.CreateTask(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/task/assign", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in AssignTaskRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.AssignTask(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/threads-for-space", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.ListThreadsForSpace(req.URL.Query().Get("space")))
	})
	mux.HandleFunc("/api/thread-detail", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetThreadDetail(req.URL.Query().Get("space"), req.URL.Query().Get("parent")))
	})
	mux.HandleFunc("/api/new-direct", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Title   string `json:"title"`
			AgentID string `json:"agent_id"`
		}
		if req.ContentLength > 0 {
			if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
				http.Error(rw, "invalid json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		out, err := b.NewDirectChat(in.Title, in.AgentID)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/events", b.handleEvents)
	mux.HandleFunc("/api/send", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in SendRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.SendMessage(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, map[string]string{"reply": out})
	})
	mux.HandleFunc("/api/message/retry", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in RetryMessageRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.RetryMessage(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, map[string]string{"reply": out})
	})
	mux.HandleFunc("/api/stop", func(rw http.ResponseWriter, req *http.Request) {
		var in struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&in)
		if in.SessionID == "" {
			in.SessionID = req.URL.Query().Get("session")
		}
		_ = b.StopTurn(in.SessionID)
		writeJSON(rw, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/personas", jsonHandler(func() any { return b.ListPersonas() }))
	mux.HandleFunc("/api/models", jsonHandler(func() any { return b.ListModels() }))
	mux.HandleFunc("/api/tools", jsonHandler(func() any { return b.ListTools() }))
	mux.HandleFunc("/api/commands", jsonHandler(func() any { return b.ListCommands() }))
	mux.HandleFunc("/api/capabilities", jsonHandler(func() any { return b.Capabilities() }))
	mux.HandleFunc("/api/memory/overview", func(rw http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		writeJSON(rw, b.MemoryOverview(q.Get("persona_id"), q.Get("source"), q.Get("space_id")))
	})
	mux.HandleFunc("/api/memory/doc", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := req.URL.Query()
		out, err := b.GetMemory(GetMemoryRequest{Scope: q.Get("scope"), ID: q.Get("id")})
		if err != nil {
			http.Error(rw, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/memory/update", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in UpdateMemoryRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.UpdateMemory(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/memory/delete", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in DeleteMemoryRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := b.DeleteMemory(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/skills", jsonHandler(func() any { return b.ListSkills() }))
	mux.HandleFunc("/api/skill", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetSkill(req.URL.Query().Get("name")))
	})
	return mux
}

func (b *Backend) handleEvents(rw http.ResponseWriter, req *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	events, cancel := b.Subscribe()
	defer cancel()
	flusher.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(rw, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(rw, "event: bus\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func jsonHandler(get func() any) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		writeJSON(rw, get())
	}
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(rw)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (b *Backend) start(ctx context.Context) {
	events, cancel := b.app.Bus().Subscribe(2048)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				ev = b.trackTurnEvent(ev)
				b.subs.publish(toBusEvent(ev))
			}
		}
	}()
}

func toBusEvent(ev bus.Event) BusEvent {
	out := BusEvent{
		Type:            ev.Type,
		Source:          ev.Source,
		SessionID:       ev.SessionID,
		TaskID:          ev.TaskID,
		RunID:           ev.RunID,
		MessageID:       ev.MessageID,
		ToolCallID:      ev.ToolCallID,
		Tool:            ev.Tool,
		Input:           ev.Input,
		Output:          ev.Output,
		Text:            ev.Text,
		Err:             ev.Err,
		Time:            ev.Time,
		SpaceID:         ev.SpaceID,
		ParentMessageID: ev.ParentMessageID,
		AgentID:         ev.AgentID,
		StreamID:        ev.StreamID,
	}
	switch ev.Type {
	case bus.DelegateQueued:
		out.Type = "agent.delegate.started"
		out.ToolCallID = "delegate-" + ev.TaskID
		out.Input = ev.Text
	case bus.DelegateStarted:
		out.Type = "agent.delegate.progress"
		out.ToolCallID = "delegate-" + ev.TaskID
		out.Text = "running"
	case bus.DelegateFinished:
		out.Type = "agent.delegate.finished"
		out.ToolCallID = "delegate-" + ev.TaskID
	case bus.DelegateFailed:
		out.Type = "agent.delegate.failed"
		out.ToolCallID = "delegate-" + ev.TaskID
	case bus.DelegateCanceled:
		out.Type = "agent.delegate.canceled"
		out.ToolCallID = "delegate-" + ev.TaskID
	case bus.ToolCallStarted, bus.ToolCallFinished, bus.ToolCallFailed:
		switch ev.Tool {
		case "mention", "spawn", "spawn_specialist", "invite_agent":
			if ev.Type == bus.ToolCallStarted {
				out.Type = "agent.mention"
			} else if ev.Type == bus.ToolCallFinished {
				out.Type = "agent.mention.reply"
				if isSchedulingAck(ev.Output) {
					out.Output = ""
				}
			} else {
				out.Type = "agent.mention.reply"
				if out.Output == "" {
					out.Output = "(failed: " + ev.Err + ")"
				}
			}
			out.Tool = mentionTarget(ev)
		}
	}
	return out
}

func isSchedulingAck(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(low, "scheduled ") || strings.Contains(low, "task_id=")
}

func mentionTarget(ev bus.Event) string {
	if ev.Input == "" {
		return ev.Tool
	}
	var args struct {
		Target string `json:"target"`
		Agent  string `json:"agent"`
		Name   string `json:"name"`
		To     string `json:"to"`
	}
	if err := json.Unmarshal([]byte(ev.Input), &args); err != nil {
		return ev.Tool
	}
	for _, v := range []string{args.Target, args.Agent, args.Name, args.To} {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ev.Tool
}

func roleFor(m msg.Message) string {
	switch m.Role {
	case "user":
		return "user"
	case "assistant":
		return "agent"
	case "system":
		return "system"
	}
	return m.Role
}

func isCollabTool(name string) bool {
	switch name {
	case "mention", "spawn", "spawn_specialist", "invite_agent":
		return true
	}
	return false
}

func mentionTargetFromArgs(rawArgs []byte, fallback string) string {
	if len(rawArgs) == 0 {
		return fallback
	}
	var args struct {
		Target  string `json:"target"`
		Agent   string `json:"agent"`
		AgentID string `json:"agent_id"`
		Name    string `json:"name"`
		To      string `json:"to"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return fallback
	}
	for _, v := range []string{args.Target, args.Agent, args.AgentID, args.Name, args.To} {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return fallback
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func personaIDs(a *app.App) []string {
	out := make([]string, 0)
	for _, p := range a.Personas().List() {
		out = append(out, p.ID)
	}
	return out
}

func isThreadID(id string) bool {
	return strings.Contains(id, "-") && !strings.HasPrefix(id, defaultChannelID)
}

func workspaceName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "workspace"
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 && i < len(path)-1 {
		return path[i+1:]
	}
	return path
}

func threadTitle(title, summary string, updated time.Time) string {
	t := strings.TrimSpace(title)
	if t != "" && !looksInternal(t) {
		return t
	}
	if s := strings.TrimSpace(summary); s != "" {
		return truncate(s, 60)
	}
	return updated.Format("Jan 2, 15:04")
}

func threadTitleFromSession(title, summary string, messages []MessageView, updated time.Time) string {
	if t := strings.TrimSpace(title); t != "" && !looksInternal(t) {
		return t
	}
	for _, m := range messages {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return truncate(strings.ReplaceAll(strings.TrimSpace(m.Content), "\n", " "), 60)
		}
	}
	if s := strings.TrimSpace(summary); s != "" {
		return truncate(s, 60)
	}
	return updated.Format("Jan 2, 15:04")
}

func looksInternal(t string) bool {
	lower := strings.ToLower(t)
	if lower == "default" || lower == "(untitled)" || strings.HasPrefix(lower, "desktop:") {
		return true
	}
	return false
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func splitModel(s string) (string, string) {
	parts := strings.SplitN(s, " / ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return s, ""
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func taskCreatedBy(tk *taskpkg.Task) string {
	if tk == nil {
		return ""
	}
	return firstNonEmpty(tk.CreatedBy, tk.InitiatorID)
}
