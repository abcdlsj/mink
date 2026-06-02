package space

import (
	"fmt"
	"strings"
	"time"
)

type RoutingTarget struct {
	AgentID string
	Chain   *RoutingChain
	Reason  string
}

type RoutingNotice struct {
	Kind      RoutingNoticeKind
	SpaceID   string
	MessageID string
	AgentID   string
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

type PersonaSnapshot func(id string) (PersonaInfo, bool)

type Router struct {
	spaces      *Manager
	chains      *ChainTracker
	persona     PersonaSnapshot
	maxMentions int
}

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

func (r *Router) resolverFunc() PersonaResolver {
	return func(token string) (string, bool) {
		token = strings.TrimSpace(token)
		if token == "" {
			return "", false
		}
		if info, ok := r.persona(token); ok && info.ID == token {
			return info.ID, true
		}
		if info, ok := r.persona(strings.ToLower(token)); ok {
			return info.ID, true
		}
		return "", false
	}
}

func (r *Router) ResolverFunc() PersonaResolver { return r.resolverFunc() }

func (r *Router) MaxMentions() int { return r.maxMentions }

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
		listening := ListeningAgentsForThread(sp, parentMessageID)
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
