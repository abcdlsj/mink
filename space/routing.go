package space

import (
	"fmt"
	"strings"
	"time"
)

// RoutingTarget is one resolved wake decision: who to wake and
// under which chain. The routing layer returns these; the caller
// is responsible for actually running the agent (typically by
// calling app.runTurnAs).
type RoutingTarget struct {
	AgentID string
	Chain   *RoutingChain
	Reason  string
}

// RoutingNotice describes a non-wake decision the routing layer
// produced — for example a no-mention channel message or a
// budget-exhausted reply. Callers can publish these onto the bus
// for the desktop UI to surface as gentle hints.
type RoutingNotice struct {
	Kind      RoutingNoticeKind
	SpaceID   string
	MessageID string
	AgentID   string // populated for per-agent notices (skipped, dup)
	At        time.Time
}

type RoutingNoticeKind string

const (
	NoticeChannelNoTarget    RoutingNoticeKind = "routing.channel.no_target"
	NoticeBudgetExhausted    RoutingNoticeKind = "routing.budget_exhausted"
	NoticeDuplicateSkipped   RoutingNoticeKind = "routing.duplicate_skipped"
	NoticeUnknownMentionDrop RoutingNoticeKind = "routing.unknown_mention"
	NoticeListeningAmbiguous RoutingNoticeKind = "routing.listening_ambiguous"
	NoticeListeningNoMatch   RoutingNoticeKind = "routing.listening_no_match"
)

// PersonaSnapshot is what the routing layer needs to look up an
// agent for atomic insertion + display copy. App layer projects
// its persona registry into this when constructing the Router.
type PersonaSnapshot func(id string) (PersonaInfo, bool)

// Router owns the routing decisions for messages that originate in
// a Space. Per Iris's lock, P2 only intercepts channel input; DM
// and direct chat continue to go through the legacy active-persona
// handoff in app.HandleInput.
type Router struct {
	spaces  *Manager
	chains  *ChainTracker
	persona PersonaSnapshot
	maxMentions int
}

// NewRouter wires the parser, manager, chain tracker, and persona
// resolver together. maxMentions <= 0 falls back to 4.
func NewRouter(m *Manager, persona PersonaSnapshot, maxMentions int) *Router {
	if maxMentions <= 0 {
		maxMentions = 4
	}
	return &Router{
		spaces:      m,
		chains:      NewChainTracker(),
		persona:     persona,
		maxMentions: maxMentions,
	}
}

// resolverFunc bridges PersonaSnapshot into the parser's
// PersonaResolver shape (id-vs-display, lowercase display, etc).
func (r *Router) resolverFunc() PersonaResolver {
	return func(token string) (string, bool) {
		token = strings.TrimSpace(token)
		if token == "" {
			return "", false
		}
		// Try id exact first.
		if info, ok := r.persona(token); ok && info.ID == token {
			return info.ID, true
		}
		// Then try the display registry by lowercase scan. The
		// PersonaSnapshot interface doesn't enumerate, so we let the
		// caller-provided snapshot do the case fold by wrapping the
		// token through a fallback id lookup with id-shape.
		if info, ok := r.persona(strings.ToLower(token)); ok {
			return info.ID, true
		}
		return "", false
	}
}

// ResolverFunc exposes the router's persona resolver so callers
// (e.g. runChannelWake) can parse mentions out of agent replies
// using the same lookup rules.
func (r *Router) ResolverFunc() PersonaResolver { return r.resolverFunc() }

// MaxMentions reports the per-message mention cap.
func (r *Router) MaxMentions() int { return r.maxMentions }

// RouteUserChannelMessage is the entry point P2 wires onto a fresh
// user message in a channel space. It parses mentions, atomically
// adds the resolved agents to the channel's participants, persists
// the user message, and starts a routing chain rooted at that
// message. The returned wake targets are everyone the chain still
// permits (resolved mentions, with budget + duplicate filtering;
// the first wake in a fresh chain always passes).
//
// notices carries any non-wake decisions for the caller to surface.
//
// IMPORTANT: this function takes the *content* of the user's input;
// it parses the mention list itself rather than trusting an external
// caller. That keeps the resolver and the participant-add path on
// the same authoritative reading of the text.
func (r *Router) RouteUserChannelMessage(spaceID, content string) ([]RoutingTarget, []RoutingNotice, error) {
	return r.RouteUserChannelMessageInThread(spaceID, content, "")
}

func (r *Router) RouteUserChannelMessageInThread(spaceID, content, parentMessageID string) ([]RoutingTarget, []RoutingNotice, error) {
	if r == nil || r.spaces == nil {
		return nil, nil, fmt.Errorf("router not configured")
	}
	mentions := ParseMentions(content, r.resolverFunc(), r.maxMentions)
	user := r.spaces.UserParticipant()
	draft := Message{
		AuthorID:        user.ID,
		AuthorKind:      ParticipantUser,
		Content:         content,
		Mentions:        mentions,
		ParentMessageID: strings.TrimSpace(parentMessageID),
	}
	written, _, err := r.spaces.AppendMessageWithRouting(spaceID, draft, mentions, func(id string) PersonaInfo {
		if r.persona == nil {
			return PersonaInfo{ID: id}
		}
		if info, ok := r.persona(id); ok {
			return info
		}
		return PersonaInfo{ID: id}
	})
	if err != nil {
		return nil, nil, err
	}
	if len(mentions) == 0 {
		sp, _ := r.spaces.LoadSpace(spaceID)
		listening := ListeningAgents(sp)
		hits := ListenMatches(content, listening)
		if len(hits) == 1 {
			chain := r.chains.Start(written.ID, spaceID, DefaultRoutingBudget)
			chain.ParentMessageID = strings.TrimSpace(parentMessageID)
			return r.fanOutListening(chain, hits[0], spaceID, written.ID)
		}
		notice := RoutingNotice{
			Kind:      NoticeChannelNoTarget,
			SpaceID:   spaceID,
			MessageID: written.ID,
			At:        time.Now(),
		}
		switch {
		case len(hits) > 1:
			notice.Kind = NoticeListeningAmbiguous
		case len(listening) > 0:
			notice.Kind = NoticeListeningNoMatch
		}
		return nil, []RoutingNotice{notice}, nil
	}
	chain := r.chains.Start(written.ID, spaceID, DefaultRoutingBudget)
	chain.ParentMessageID = strings.TrimSpace(parentMessageID)
	return r.fanOut(chain, mentions, spaceID, written.ID)
}

// RouteAgentReply is invoked after an agent appends a reply via the
// manager. It looks for @-mentions in that reply and decides which
// (if any) further agents to wake under the same chain — preserving
// budget so multi-agent loops self-terminate.
//
// chainRoot identifies the user message that started the cascade.
// If no chain exists for that root (e.g. the reply landed late),
// the function returns no targets and a single budget_exhausted
// notice so the bus surfaces a clean explanation.
func (r *Router) RouteAgentReply(spaceID, chainRoot, replyMessageID, replyContent, replyingAgentID string) ([]RoutingTarget, []RoutingNotice, error) {
	if r == nil || r.spaces == nil {
		return nil, nil, fmt.Errorf("router not configured")
	}
	chain := r.chains.Get(chainRoot)
	if chain == nil {
		return nil, []RoutingNotice{{
			Kind:      NoticeBudgetExhausted,
			SpaceID:   spaceID,
			MessageID: replyMessageID,
			At:        time.Now(),
		}}, nil
	}
	mentions := ParseMentions(replyContent, r.resolverFunc(), r.maxMentions)
	if len(mentions) == 0 {
		return nil, nil, nil
	}
	// Don't allow an agent to wake itself recursively.
	filtered := make([]string, 0, len(mentions))
	for _, id := range mentions {
		if id != replyingAgentID {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		return nil, nil, nil
	}
	return r.fanOut(chain, filtered, spaceID, replyMessageID)
}

// fanOut runs the per-chain admission filter over a list of agent
// ids and returns the resulting wake targets + notices. It is
// shared by user routing and agent-reply routing so the budget /
// duplicate rules are identical at every entry.
func (r *Router) fanOut(chain *RoutingChain, agents []string, spaceID, originMessageID string) ([]RoutingTarget, []RoutingNotice, error) {
	wakes := make([]RoutingTarget, 0, len(agents))
	notices := make([]RoutingNotice, 0)
	for _, id := range agents {
		ok, why := chain.CanWake(id)
		if !ok {
			notices = append(notices, RoutingNotice{
				Kind:      noticeKindFor(why),
				SpaceID:   spaceID,
				MessageID: originMessageID,
				AgentID:   id,
				At:        time.Now(),
			})
			continue
		}
		chain.Spend(id)
		wakes = append(wakes, RoutingTarget{AgentID: id, Chain: chain})
	}
	return wakes, notices, nil
}

func (r *Router) fanOutListening(chain *RoutingChain, agentID, spaceID, originMessageID string) ([]RoutingTarget, []RoutingNotice, error) {
	if ok, _ := chain.CanWake(agentID); !ok {
		return nil, []RoutingNotice{{
			Kind:      NoticeChannelNoTarget,
			SpaceID:   spaceID,
			MessageID: originMessageID,
			At:        time.Now(),
		}}, nil
	}
	chain.Spend(agentID)
	return []RoutingTarget{{
		AgentID: agentID,
		Chain:   chain,
		Reason:  "joined from channel listening",
	}}, nil, nil
}

func noticeKindFor(reason string) RoutingNoticeKind {
	switch reason {
	case "budget_exhausted":
		return NoticeBudgetExhausted
	case "duplicate_skipped":
		return NoticeDuplicateSkipped
	default:
		return NoticeBudgetExhausted
	}
}
