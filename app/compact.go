package app

import (
	"context"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/textutil"
)

func (a *App) compactSession(ctx context.Context, s *session.Session) (string, error) {
	return a.compactSessionKeep(ctx, s, 8)
}

func (a *App) compactSessionKeep(ctx context.Context, s *session.Session, keep int) (string, error) {
	if len(s.Messages) == 0 {
		return "empty session", nil
	}
	summary, err := a.buildCompactSummary(ctx, s)
	if err != nil {
		return "", err
	}
	summary = summaryWithProvenance(summary, summaryProvenance{
		Profile:      ContextProfileDirect,
		MessageCount: len(s.Messages),
	}, time.Time{}, time.Now())
	s.Compact(summary, keep)
	return s.Summary, nil
}

func (a *App) buildCompactSummary(ctx context.Context, s *session.Session) (string, error) {
	return a.buildCompactSummaryFor(ctx, s.Messages)
}

// buildCompactSummaryFor summarizes an explicit message slice. autoCompact feeds
// it the compacted prefix of the Space projection (not s.Messages) so the raw
// summary always covers exactly the folded prefix, independent of any prior
// checkpoint replay state on the session.
func (a *App) buildCompactSummaryFor(ctx context.Context, msgs []msg.Message) (string, error) {
	if len(msgs) == 0 {
		return "empty session", nil
	}
	var b strings.Builder
	for _, m := range msgs {
		if !eligibleSessionSummaryMessage(m) {
			continue
		}
		switch m.Role {
		case "user", "assistant":
			b.WriteString(m.Role + ": " + m.Content + "\n")
		case "tool":
			for _, tr := range m.ToolResults {
				b.WriteString("tool: " + tr.Content + "\n")
			}
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "No eligible prior messages after runtime noise filtering.", nil
	}
	if a.provider != nil {
		resp, err := a.provider.Chat(ctx, []msg.Message{
			{Role: "system", Content: "Summarize the conversation for future continuation. Keep it short and factual."},
			{Role: "user", Content: b.String()},
		}, nil)
		if err == nil {
			return strings.TrimSpace(resp.Content), nil
		}
	}
	return heuristicSummary(msgs), nil
}

func eligibleSessionSummaryMessage(m msg.Message) bool {
	if m.Role == "tool" {
		for _, tr := range m.ToolResults {
			if strings.TrimSpace(tr.Error) != "" {
				return false
			}
		}
	}
	content := strings.TrimSpace(primaryText(m))
	if content == "" {
		return false
	}
	if content == "NO_REPLY" || strings.HasPrefix(content, "NO_REPLY ") {
		return false
	}
	if noisyRuntimeContent(content) {
		return false
	}
	return true
}

func (a *App) autoCompact(ctx context.Context, source, runtime string, s *session.Session, view ContextView) error {
	if !a.shouldAutoCompact(runtime, s) {
		return nil
	}
	// A projection-backed turn (persisted Space) records a checkpoint so the raw
	// summary and its compact boundary survive into later rounds; the deterministic
	// rebuild in view.Apply then replays [summary] + un-compacted suffix instead of
	// re-loading and re-compacting the whole history every turn. A turn with no
	// Space (in-memory Draft) has nothing to project from, so it keeps the legacy
	// in-place compaction and writes no checkpoint.
	if strings.TrimSpace(view.SpaceID) != "" {
		return a.autoCompactWithCheckpoint(ctx, source, s, view)
	}
	summary, err := a.compactSessionKeep(ctx, s, a.cfg.Compact.KeepRecentMessages)
	if err != nil {
		return err
	}
	if err := a.sessions.Save(s); err != nil {
		return err
	}
	a.bus.Publish(bus.Event{
		Type:      bus.SessionCompacted,
		Source:    source,
		SessionID: s.ID,
		Text:      summary,
	})
	return nil
}

// autoCompactWithCheckpoint folds the compacted prefix of the Space projection
// into a persistent checkpoint. The boundary is computed against view.Messages
// (the full, un-checkpointed Space projection with stable Space IDs) so it stays
// consistent with resolveCheckpoint on every later round.
func (a *App) autoCompactWithCheckpoint(ctx context.Context, source string, s *session.Session, view ContextView) error {
	keep := a.cfg.Compact.KeepRecentMessages
	if keep < 0 {
		keep = 8
	}
	full := view.Messages
	if len(full) <= keep {
		// Nothing ahead of the keep-recent window to fold; leave the session as
		// the full projection rather than writing an empty-prefix checkpoint.
		return nil
	}
	prefix := full[:len(full)-keep]
	boundary := prefix[len(prefix)-1]
	if strings.TrimSpace(boundary.ID) == "" {
		// Cannot anchor a checkpoint without a stable Space message ID; fall back
		// to legacy in-place compaction for this turn.
		summary, err := a.compactSessionKeep(ctx, s, keep)
		if err != nil {
			return err
		}
		if err := a.sessions.Save(s); err != nil {
			return err
		}
		a.bus.Publish(bus.Event{Type: bus.SessionCompacted, Source: source, SessionID: s.ID, Text: summary})
		return nil
	}

	raw, err := a.buildCompactSummaryFor(ctx, prefix)
	if err != nil {
		return err
	}
	s.Checkpoint = &session.ProjectionCheckpoint{
		SpaceID:                 view.SpaceID,
		ParentMessageID:         view.ParentMessageID,
		AgentID:                 view.AgentID,
		Profile:                 string(view.Profile),
		SummaryThroughMessageID: boundary.ID,
		Summary:                 raw,
		PrefixFingerprint:       fingerprintMessages(prefix),
	}
	// Replay immediately so this round's runtime context is byte-identical to the
	// [summary] + suffix that every subsequent round will rebuild.
	view.Apply(s)
	if err := a.sessions.Save(s); err != nil {
		return err
	}
	a.bus.Publish(bus.Event{
		Type:      bus.SessionCompacted,
		Source:    source,
		SessionID: s.ID,
		Text:      s.Summary,
	})
	return nil
}

func (a *App) shouldAutoCompact(runtime string, s *session.Session) bool {
	if !a.cfg.Compact.Auto || s == nil || len(s.Messages) == 0 {
		return false
	}
	if isExternalDriverRuntime(runtime) {
		return false
	}
	if n := a.cfg.Compact.TriggerMessages; n > 0 && len(s.Messages) >= n {
		return true
	}
	if n := a.compactTokenLimit(); n > 0 && estimateMessages(s.Messages) >= n {
		return true
	}
	return false
}

func (a *App) compactTokenLimit() int {
	mc := a.cfg.Active
	if mc.ContextWindow > 0 {
		limit := mc.ContextWindow - max(mc.MaxTokens, a.cfg.MaxTokens) - a.cfg.Compact.ReserveTokens
		if limit > 0 {
			return limit
		}
	}
	if a.cfg.Compact.TriggerTokens > 0 {
		return a.cfg.Compact.TriggerTokens
	}
	return 0
}

func isExternalDriverRuntime(runtime string) bool {
	switch strings.TrimSpace(runtime) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func heuristicSummary(msgs []msg.Message) string {
	var b strings.Builder
	start := 0
	if len(msgs) > 12 {
		start = len(msgs) - 12
	}
	for _, m := range msgs[start:] {
		text := primaryText(m)
		if text == "" {
			continue
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(trimText(text, 160))
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "Conversation compacted at " + time.Now().Format(time.RFC3339)
	}
	return out
}

func primaryText(m msg.Message) string {
	switch {
	case strings.TrimSpace(m.Content) != "":
		return m.Content
	case strings.TrimSpace(m.Reasoning) != "":
		return m.Reasoning
	case len(m.ToolCalls) > 0:
		var parts []string
		for _, tc := range m.ToolCalls {
			parts = append(parts, tc.Name+"("+strings.TrimSpace(string(tc.Args))+")")
		}
		return strings.Join(parts, "; ")
	case len(m.ToolResults) > 0:
		var parts []string
		for _, tr := range m.ToolResults {
			part := tr.Content
			if strings.TrimSpace(tr.Error) != "" {
				part = "error: " + tr.Error
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, "; ")
	default:
		return ""
	}
}

func estimateMessages(msgs []msg.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessage(m)
	}
	return total
}

func estimateMessage(m msg.Message) int {
	n := len([]rune(m.Content)) + len([]rune(m.Reasoning))
	for _, tc := range m.ToolCalls {
		n += len([]rune(tc.Name)) + len([]rune(string(tc.Args)))
	}
	for _, tr := range m.ToolResults {
		n += len([]rune(tr.Content)) + len([]rune(tr.Error))
	}
	if n == 0 {
		return 0
	}
	return n/4 + 1
}

func trimText(s string, n int) string {
	return textutil.Ellipsis(strings.Join(strings.Fields(strings.TrimSpace(s)), " "), n)
}
