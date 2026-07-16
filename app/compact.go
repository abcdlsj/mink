package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/textutil"
)

// ErrContextOverflow is returned when the projected runtime input (history +
// pending user turn + reserve) cannot be brought under the model context window
// even after compaction. It is a hard, visible failure: the compact path is
// forbidden from silently truncating history or splicing a heuristic summary to
// fake success, so callers surface this to the user instead.
var ErrContextOverflow = errors.New("input/context exceeds model window after compact")

func contextOverflowError(detail string) error {
	if strings.TrimSpace(detail) == "" {
		return ErrContextOverflow
	}
	return fmt.Errorf("%w: %s", ErrContextOverflow, detail)
}

const (
	// summaryCallBudgetFallback bounds a single summarize call when the active
	// model advertises no context window (external drivers), so a very long
	// prefix is still chunked rather than fed raw to the summarizer.
	summaryCallBudgetFallback = 8000
	// maxSummarizeIterations guards the accumulator-fold loop against a
	// summarizer that refuses to shrink its own output below budget.
	maxSummarizeIterations = 256
	// minSummarizeChunkRoom is the smallest per-call room (in estimated tokens)
	// that can still make progress. splitLineToRoom floors a forced piece at 4
	// runes, and takeChunk charges estimateText(4)+1 = 3 tokens for it, so a room
	// below 3 can never accept even the smallest split piece — it would re-split
	// to the same 4-rune floor forever until the iteration guard trips. When the
	// running summary has eaten the budget down this far, fail closed instead.
	minSummarizeChunkRoom = 3
	// summarizeChunkFixedOverhead is the per-call token headroom reserved on top
	// of the prior-summary and system-prompt estimates for the call's fixed
	// wrapper (role framing, separators). Subtracted when computing per-call room.
	summarizeChunkFixedOverhead = 64
	// perImageBudgetTokens is a conservative, fixed capacity estimate charged for
	// each image attachment on the pending turn. Images do not expand into the
	// text content (they are handed to the runtime as native image parts), so the
	// char/4 text heuristic misses them entirely; without a floor a turn carrying
	// several large images reads as ~0 pending tokens and slips past the hard
	// guard. This is a capacity estimate for overflow accounting, NOT a summary
	// fallback — it deliberately errs high so the guard trips early rather than
	// late. Real per-model image tokenization varies; this floor is intentionally
	// coarse and provider-agnostic.
	perImageBudgetTokens = 1024
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
//
// The summarizer self-guards the model window: a very long prefix is never fed
// raw to the provider in one call (that call would itself overflow). Instead the
// prefix is split into chunks whose per-call token estimate stays under the
// summarize-call budget, and each chunk is folded into a running summary that
// participates as accumulator state. When no provider is available or a
// summarize call fails, this returns an error — it must NOT fall back to a
// silent heuristic summary, because that would let compaction fake success.
func (a *App) buildCompactSummaryFor(ctx context.Context, msgs []msg.Message) (string, error) {
	if len(msgs) == 0 {
		return "empty session", nil
	}
	lines := summarySourceLines(msgs)
	if len(lines) == 0 {
		return "No eligible prior messages after runtime noise filtering.", nil
	}
	if a.provider == nil {
		return "", errors.New("cannot compact: no summarization provider configured")
	}
	return a.summarizeChunked(ctx, "", lines)
}

// summarySourceLines flattens eligible messages into role-tagged lines. Each
// line is an independently splittable unit for chunking.
func summarySourceLines(msgs []msg.Message) []string {
	var lines []string
	for _, m := range msgs {
		if !eligibleSessionSummaryMessage(m) {
			continue
		}
		switch m.Role {
		case "user", "assistant":
			if s := strings.TrimSpace(m.Content); s != "" {
				lines = append(lines, m.Role+": "+m.Content)
			}
		case "tool":
			for _, tr := range m.ToolResults {
				if s := strings.TrimSpace(tr.Content); s != "" {
					lines = append(lines, "tool: "+tr.Content)
				}
			}
		}
	}
	return lines
}

const summarizeSystemPrompt = "Summarize the conversation for future continuation. Keep it short and factual. If a prior summary is provided, extend it with the new messages into a single coherent summary."

// summarizeChunked folds source lines into a running summary, keeping every
// provider call's input under the summarize-call budget. prior is the summary
// carried over from earlier chunks (the accumulator); it is included in each
// call so context is preserved across chunk boundaries.
func (a *App) summarizeChunked(ctx context.Context, prior string, lines []string) (string, error) {
	budget := a.summarizeCallBudget()
	iterations := 0
	i := 0
	for i < len(lines) {
		iterations++
		if iterations > maxSummarizeIterations {
			return "", contextOverflowError("summarizer did not converge below the per-call budget")
		}
		// Room for new-message text in this call = budget - (prior summary +
		// system prompt + fixed wrapper). The running summary participates as
		// accumulator state, so as it grows the room shrinks. If the accumulator
		// alone can no longer leave room for any new content, no chunking helps:
		// fail closed rather than emit a call that overflows the budget.
		room := summarizeChunkRoom(budget, prior)
		if room < minSummarizeChunkRoom {
			return "", contextOverflowError("running summary no longer leaves room for even the smallest chunk under the per-call budget")
		}
		chunk, next := takeChunk(lines, i, room)
		if len(chunk) == 0 {
			// A single line still exceeds room; rune-split it and splice the
			// pieces back into the stream so each is folded by its OWN bounded
			// call (layering, not truncation, and never one oversized payload).
			pieces := splitLineToRoom(lines[i], room)
			rest := append(pieces, lines[i+1:]...)
			lines = append(lines[:i:i], rest...)
			continue
		}
		summary, err := a.summarizeCall(ctx, prior, chunk)
		if err != nil {
			return "", err
		}
		prior = summary
		i = next
	}
	return strings.TrimSpace(prior), nil
}

// summarizeChunkRoom computes the estimated-token room left for NEW content in a
// single summarize call, given the total per-call budget and the running prior
// summary that must be re-sent as accumulator state. It is the sole definition of
// the room formula so the fail-closed boundary (compared against
// minSummarizeChunkRoom) can be exercised directly. May return <= 0 when the
// accumulator alone already exceeds the budget.
func summarizeChunkRoom(budget int, prior string) int {
	return budget - estimateText(prior) - estimateText(summarizeSystemPrompt) - summarizeChunkFixedOverhead
}

// takeChunk returns the maximal run of whole lines starting at i whose combined
// token estimate fits room, along with the next index. It returns an empty
// chunk when even lines[i] alone exceeds room (caller must split it).
func takeChunk(lines []string, i, room int) ([]string, int) {
	total := 0
	j := i
	for j < len(lines) {
		t := estimateText(lines[j]) + 1
		if j > i && total+t > room {
			break
		}
		if j == i && t > room {
			return nil, i
		}
		total += t
		j++
	}
	return lines[i:j], j
}

// splitLineToRoom breaks a single over-budget line into rune-bounded pieces that
// each fit within room tokens (estimated as runes/4+1). The pieces are spliced
// back into the line stream by the caller so every piece is summarized by its
// own bounded provider call — layering the content across sequential calls
// rather than dropping any of it or feeding one oversized payload to the model.
func splitLineToRoom(line string, room int) []string {
	// takeChunk accepts a single line only when estimate(k)+1 <= room, and
	// estimate(k)=k/4+1, so a piece is accepted iff k/4+1+1 <= room, i.e.
	// k <= (room-2)*4. Size pieces to that ceiling so each is accepted by the
	// very next takeChunk instead of being re-split forever.
	runesPerChunk := (room - 2) * 4
	if runesPerChunk < 4 {
		runesPerChunk = 4
	}
	rs := []rune(line)
	var pieces []string
	for start := 0; start < len(rs); start += runesPerChunk {
		end := start + runesPerChunk
		if end > len(rs) {
			end = len(rs)
		}
		pieces = append(pieces, string(rs[start:end]))
	}
	return pieces
}

// summarizeCall runs one provider summarize over prior + chunk. It is the single
// authoritative path; on provider error it returns the error (no heuristic
// fallback).
func (a *App) summarizeCall(ctx context.Context, prior string, chunk []string) (string, error) {
	var b strings.Builder
	if strings.TrimSpace(prior) != "" {
		b.WriteString("Prior summary:\n")
		b.WriteString(prior)
		b.WriteString("\n\nNew messages:\n")
	}
	for _, line := range chunk {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	resp, err := a.provider.Chat(ctx, []msg.Message{
		{Role: "system", Content: summarizeSystemPrompt},
		{Role: "user", Content: b.String()},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("summarize call failed: %w", err)
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" {
		return "", errors.New("summarize call returned empty summary")
	}
	return out, nil
}

// summarizeCallBudget is the per-call input token ceiling for summarization.
// It tracks the model's usable input window (compactTokenLimit) when known, and
// falls back to a fixed budget for external/unconfigured models so chunking
// still happens.
func (a *App) summarizeCallBudget() int {
	if n := a.compactTokenLimit(); n > 0 {
		return n
	}
	return summaryCallBudgetFallback
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

// autoCompact is called before the runtime builds this turn's prompt, with the
// pending user input and its attachments that engine/external will append to the
// session AFTER this returns. The pending turn therefore never participates in
// the summary (only already-persisted Space history does) but IS counted against
// the model window so the hard-overflow guard sees the true projected input —
// including quoted_transcript attachments (which expand into the pending message
// text) and images (charged a conservative per-image capacity budget). See
// estimatePendingTurn.
//
// A projection-backed turn (persisted Space) records a checkpoint so the raw
// summary and its compact boundary survive into later rounds; the deterministic
// rebuild in view.Apply then replays [summary] + un-compacted suffix. The
// hard-overflow guard runs on every Space-backed turn regardless of Compact.Auto,
// but only when there is an enforceable input ceiling for the consuming runtime:
// native derives it from the active model's window; an external driver has one
// ONLY when the operator declared config.ExternalInputBudgets[runtime] — absent
// that, the external driver is unguarded and owns its own overflow (see
// hardBudgetStatusFor). A turn with no Space (in-memory Draft / desktop) has
// nothing to project from and keeps the legacy Auto-gated in-place compaction —
// desktop behavior is unchanged.
func (a *App) autoCompact(ctx context.Context, source, runtime string, s *session.Session, view ContextView, pendingInput string, pendingAttachments []msg.Attachment) error {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(view.SpaceID) != "" {
		return a.autoCompactWithCheckpoint(ctx, source, runtime, s, view, pendingInput, pendingAttachments)
	}
	// Legacy no-Space path (draft/desktop): Auto-gated, internal-only, no hard
	// window guard (no stable projection to anchor a checkpoint).
	if !a.shouldAutoCompact(runtime, s) {
		return nil
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
//
// Two triggers can start compaction: a soft trigger (Compact.Auto + message/token
// threshold) that keeps the conversation tidy, and a hard trigger (the projected
// [history]+pending+reserve exceeds the model window) that must act regardless of
// Compact.Auto. When hard overflow is in play, "keep recent" is best-effort: the
// keep window shrinks and the compacted prefix grows until the projection fits;
// if it still does not fit at keep=0 (or the pending turn alone exceeds the
// window), it returns ErrContextOverflow instead of truncating or faking success.
func (a *App) autoCompactWithCheckpoint(ctx context.Context, source, runtime string, s *session.Session, view ContextView, pendingInput string, pendingAttachments []msg.Attachment) error {
	full := view.Messages
	pendingTokens := estimatePendingTurn(pendingInput, pendingAttachments)
	// The hard guard enforces the window the RUNTIME that consumes this turn will
	// hit. For native that is the active model's derived window; for an external
	// driver it is enforceable only when the operator declared a ceiling via
	// config (ExternalInputBudgets), otherwise the guard stands down and the
	// driver owns its own overflow. summarizeCallBudget below stays on the active
	// (summarizer) model's window — the two sources are intentionally distinct.
	budget, enforceable, err := a.hardBudgetStatusFor(runtime)
	if err != nil {
		// A window is declared but leaves no usable input budget. This is a broken
		// config, not an unknown window: fail loud instead of letting the turn
		// through unguarded. Only surface it when there is a real ceiling to
		// enforce (view.SpaceID set) — the no-Space legacy path has no hard guard.
		if view.SpaceID != "" {
			return err
		}
		budget, enforceable = 0, false
	}
	hard := enforceable && budget > 0 && a.projectedInputTokens(s, pendingTokens) > budget
	soft := a.shouldAutoCompact(runtime, s)
	if !hard && !soft {
		return nil
	}

	// Pending turn + reserve alone cannot fit: no amount of history compaction
	// helps, so fail closed before compacting.
	if hard && pendingTokens >= budget {
		return contextOverflowError(fmt.Sprintf("pending input (~%d tok) alone exceeds usable window (~%d tok)", pendingTokens, budget))
	}

	keep := a.cfg.Compact.KeepRecentMessages
	if keep < 0 {
		keep = 8
	}
	if keep > len(full) {
		keep = len(full)
	}

	for {
		if len(full) <= keep {
			// Nothing ahead of the keep-recent window to fold.
			if !hard {
				return nil
			}
			// Hard overflow with nothing left to compact: the suffix + pending +
			// reserve genuinely does not fit.
			if a.projectedInputTokens(s, pendingTokens) > budget {
				return contextOverflowError("history compressed to no prior context and still exceeds the model window")
			}
			return nil
		}
		prefix := full[:len(full)-keep]
		boundary := prefix[len(prefix)-1]
		if strings.TrimSpace(boundary.ID) == "" {
			return a.autoCompactLegacyInPlace(ctx, source, s, keep)
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
		// Replay immediately so this round's runtime context is byte-identical to
		// the [summary] + suffix that every subsequent round will rebuild.
		view.Apply(s)

		if !hard || a.projectedInputTokens(s, pendingTokens) <= budget {
			break
		}
		// Still over the window after this compact: shrink the keep window so the
		// next pass folds more history into the summary. keep=0 means the whole
		// history is behind the boundary; if that still overflows, the loop head
		// returns ErrContextOverflow.
		if keep == 0 {
			return contextOverflowError("history compressed to summary-only and still exceeds the model window")
		}
		keep /= 2
	}

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

// autoCompactLegacyInPlace handles the rare case where the compact boundary has
// no stable Space message ID (cannot anchor a checkpoint): fall back to legacy
// in-place compaction for this turn.
func (a *App) autoCompactLegacyInPlace(ctx context.Context, source string, s *session.Session, keep int) error {
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

// projectedInputTokens estimates the runtime input for a turn: the current
// session messages (already the [summary]+suffix projection after any Apply)
// plus the pending user turn that the runtime will append next.
func (a *App) projectedInputTokens(s *session.Session, pendingTokens int) int {
	return estimateMessages(s.Messages) + pendingTokens
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
	if limit := a.hardInputBudget(); limit > 0 {
		return limit
	}
	if a.cfg.Compact.TriggerTokens > 0 {
		return a.cfg.Compact.TriggerTokens
	}
	return 0
}

// hardInputBudget is the usable input-token ceiling derived from the real model
// context window: ContextWindow minus the reserved output tokens and a reserve
// margin. It is the basis for the hard-overflow guard, so it deliberately does
// NOT fall back to the soft TriggerTokens config. It returns 0 whenever there is
// no enforceable budget (unknown window OR declared-but-unusable); callers that
// must distinguish those two cases use hardBudgetStatus instead.
func (a *App) hardInputBudget() int {
	budget, _, _ := a.hardBudgetStatus()
	return budget
}

// hardBudgetStatus resolves the hard-overflow budget into three distinct states,
// because "we cannot enforce a ceiling" and "the configured ceiling is broken"
// are not the same thing and must not be conflated into a silent 0:
//
//   - enforceable=true, err=nil: a real, usable budget (>0).
//   - enforceable=false, err=nil: the active model advertises no context window
//     (ContextWindow<=0 — external drivers / unconfigured). Honestly unknown, so
//     the hard guard stands down; the driver owns its own context.
//   - err!=nil: a window IS declared but MaxTokens+ReserveTokens leave no room for
//     any input (reserve >= window). That is a misconfiguration, not an unknown
//     window, so it surfaces as an explicit error rather than silently disabling
//     the guard (which would let overflow through under a config that promised a
//     ceiling).
func (a *App) hardBudgetStatus() (budget int, enforceable bool, err error) {
	mc := a.cfg.Active
	if mc.ContextWindow <= 0 {
		return 0, false, nil
	}
	reserve := max(mc.MaxTokens, a.cfg.MaxTokens) + a.cfg.Compact.ReserveTokens
	limit := mc.ContextWindow - reserve
	if limit <= 0 {
		return 0, false, contextOverflowError(fmt.Sprintf("model context window (~%d tok) leaves no usable input budget after reserving output+margin (~%d tok); raise ContextWindow or lower MaxTokens/ReserveTokens", mc.ContextWindow, reserve))
	}
	return limit, true, nil
}

// hardBudgetStatusFor resolves the hard-overflow INPUT budget for the runtime
// that will actually consume this turn's prompt. The consumer window and the
// summarizer's own window are two separate sources and must never be conflated:
//
//   - A native runtime consumes the active model directly, so its usable input
//     ceiling is derived from that model's context window (hardBudgetStatus:
//     ContextWindow - output reserve - margin). This is also the summarizer's own
//     window, since the summarizer runs on the same active model.
//   - An external driver runtime (claude/codex) runs its own model behind a CLI.
//     Its real context window is owned by the driver and is NOT the active
//     (summarizer) model's window, so we do not guess it by subtracting native
//     MaxTokens/ReserveTokens. The hard guard is enforceable for it ONLY when the
//     operator declares a ceiling via config.ExternalInputBudgets[runtime]; that
//     value is the usable input ceiling directly. When it is absent the guard is
//     UNGUARDED — (0, false, nil) — and the external driver owns and reports its
//     own overflow. Unknown is not an error here (see Iris's ruling); make it a
//     config error later only if unknown-must-fail-closed is required.
func (a *App) hardBudgetStatusFor(runtime string) (budget int, enforceable bool, err error) {
	if externalRuntimeName(runtime) {
		key := strings.ToLower(strings.TrimSpace(runtime))
		if limit := a.cfg.ExternalInputBudgets[key]; limit > 0 {
			return limit, true, nil
		}
		return 0, false, nil
	}
	return a.hardBudgetStatus()
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

// estimatePendingTurn estimates the tokens the pending user turn will actually
// occupy once the runtime builds it. It must match how the turn is really
// materialized: engine/external append agent.NewUserMessageWithAttachments,
// whose Content is agent.UserInputWithAttachments(text, attachments) — that
// EXPANDS every quoted_transcript attachment's Data into the text body. Counting
// only the raw input would miss a short prompt carrying a huge imported
// transcript, letting it slip past the hard guard. Image attachments do not
// expand into text (they go to the runtime as native image parts), so they are
// charged a conservative per-image capacity budget instead.
func estimatePendingTurn(text string, attachments []msg.Attachment) int {
	expanded := agent.UserInputWithAttachments(text, attachments)
	n := estimateText(expanded)
	for _, at := range attachments {
		if at.Kind == "image" {
			n += perImageBudgetTokens
		}
	}
	return n
}

// estimateText approximates the token count of a plain string with the same
// chars/4 heuristic used for messages.
func estimateText(s string) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	return n/4 + 1
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
