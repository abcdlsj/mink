package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

// channelRouter is the lazily-built Router used by inputFlow when
// a source maps to a channel space. It is constructed once per App
// and survives reloads of the persona registry — the snapshot
// closure asks the registry on every lookup.
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
			// Try lower-case fallback (display match path used by parser).
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

// sourceIsChannel is kept for backwards-compat with the few call
// sites that strictly mean "channel kind" (not direct chat). New
// callers should use sourceUsesRouter.
func sourceIsChannel(source string) bool {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "subtask:") || strings.HasPrefix(source, "scratch:") {
		return false
	}
	target := space.MapSource(source)
	return target.Kind == space.KindChannel
}

// channelInterceptResult captures everything the input flow needs
// to know after deferring channel handling to the router. wakes
// is the list of routing decisions; notices is the list of
// non-wake outcomes for the bus to surface (P2.5c will publish).
//
// In P2.5a wakes is computed but never executed; runtime invocation
// arrives in P2.5b.
type channelInterceptResult struct {
	spaceID string
	wakes   []space.RoutingTarget
	notices []space.RoutingNotice
}

// interceptRoutedInput is the P2.5a/b/P3.5 entry point. It writes
// the user message to the Space via the router (which also handles
// atomic participant insertion) and runs every wake target the
// router decided on. Each wake produces one ephemeral runtime turn
// whose assistant output is mirrored back into the same Space as
// an agent-authored message; the agent's reply is then re-routed
// so further @-mentions wake more agents within the same chain
// and budget.
//
// In P3.5 this entry handles both Channel and DirectChat kinds
// (anything that satisfies sourceUsesRouter). The earlier name
// "interceptChannelInput" was misleading once Direct Chats joined.
//
// Errors propagate: if the user-message Space write fails, the
// caller sees the failure rather than a half-applied turn.
// Per-wake runtime errors are recorded but do NOT fail the user
// message — Iris's amendment 5: "budget/duplicate notice 不要变成
// error，也不要让用户消息失败".
func (a *App) interceptRoutedInput(ctx context.Context, source, content string) (*channelInterceptResult, error) {
	r := a.channelRouter()
	if r == nil {
		return nil, nil
	}
	target := space.MapSource(source)
	if target.Kind != space.KindChannel && target.Kind != space.KindDirectChat {
		return nil, nil
	}
	sp, err := a.spaces.EnsureSpace(target.Kind, target.Seed, space.PersonaInfo{})
	if err != nil {
		return nil, err
	}
	wakes, notices, err := r.RouteUserChannelMessage(sp.ID, content)
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

// interceptChannelInput is preserved as a thin alias so any legacy
// caller / test names still compile. New code should call
// interceptRoutedInput directly.
func (a *App) interceptChannelInput(ctx context.Context, source, content string) (*channelInterceptResult, error) {
	return a.interceptRoutedInput(ctx, source, content)
}

// runChannelWake fires one agent's turn against the channel Space.
// It uses a brand-new in-memory session.Session that is never given
// to the manager, never saved to disk, never returned by ListSessions
// — Iris's hard rule for ephemeral runtime scratch.
//
// After the runtime returns, the assistant output (content +
// reasoning, concatenated across multi-message streams) is written
// to the Space via space.Manager.AppendAgentMessage. Empty replies
// and tool-only turns produce no Space message. Any further
// @-mentions in the reply are then dispatched through
// router.RouteAgentReply within the same chain so subsequent agent
// fanout reuses the same budget.
func (a *App) runChannelWake(ctx context.Context, originSource, spaceID string, target space.RoutingTarget, originUserContent string) []space.RoutingNotice {
	persona := a.personas.Get(target.AgentID)
	if persona == nil {
		// resolver should have screened this, but be defensive.
		return nil
	}
	scratch := session.New("scratch:wake:" + uuid.NewString()[:8])
	// Seed scratch with the user message so the runtime sees what
	// it's responding to. P2 only carries the originating user line;
	// full Space timeline goes in once P3's adapter loads it.
	scratch.Add(msg.Message{
		Role:    "user",
		Content: originUserContent,
	})
	baseline := len(scratch.Messages)
	rt, err := a.newRuntimeFor(persona.Runtime, persona)
	if err != nil {
		return nil
	}
	// Run directly — bypass turnFlow so sessions.Save is never called
	// against the scratch session.
	_ = rt.Run(ctx, &agent.Turn{
		Source:  scratch.Source,
		Input:   originUserContent,
		Session: scratch,
		Bus:     a.bus,
	})
	content, reasoning := assembleAssistantOutput(scratch.Messages[baseline:])
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		return nil
	}
	r := a.channelRouter()
	if r == nil {
		return nil
	}
	// Resolve mentions in the agent's own reply *before* writing so
	// the message lands in one atomic write that also adds any new
	// participant. This is the second leg of Iris's "@ adds to
	// membership atomically" rule — the first leg fires for user
	// messages in RouteUserChannelMessage.
	resolved := space.ParseMentions(content, r.ResolverFunc(), r.MaxMentions())
	// Self-mentions cannot wake the replying agent and must not
	// re-add it (it's already a participant); strip before atomic
	// add so AppendMessageWithRouting's resolveInfo isn't asked for
	// the speaker.
	resolved = filterOut(resolved, target.AgentID)

	draft := space.Message{
		AuthorID:   persona.ID,
		AuthorKind: space.ParticipantAgent,
		Content:    content,
		Reasoning:  reasoning,
		Mentions:   resolved,
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
	// Recurse: any @-mentions in this reply may wake further agents
	// inside the same chain. RouteAgentReply enforces budget /
	// duplicate / self-mention rules.
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

// publishRoutingNotices forwards every space.RoutingNotice onto the
// app bus as a bus.Event the desktop / CLI / TG adapters can listen
// for. Per Iris (P2.5c review):
//   - notices are hints, never errors;
//   - channel.no_target / budget_exhausted / duplicate_skipped use
//     event types directly named after the notice kind so subscribers
//     can match on Type;
//   - unknown_mention is intentionally not produced today (the
//     parser drops unknowns silently); when we add it later it will
//     ride the same channel as a quiet/debug-level signal.
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
			Tool:       n.AgentID, // reuse Tool slot for the per-agent subject
			Time:       n.At,
		})
	}
}

// filterOut returns ids without the given drop value, preserving
// order. Used to strip a self-mention from a resolved list before
// the atomic write so the speaker is not asked for participant info
// it already supplied.
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

// assembleAssistantOutput concatenates assistant role messages
// produced during a single scratch run. Per Iris's red-line:
//   - only assistant role; tool / system / user messages are dropped
//   - empty content/reasoning segments are skipped (no blank lines)
//   - segments are joined with a single newline; no author markers
//     in the body.
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
