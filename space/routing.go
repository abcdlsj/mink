package space

import (
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/msg"
)

type RoutingTarget struct {
	AgentID         string
	OriginMessageID string
	Chain           *RoutingChain
	Reason          string
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

func (r *Router) RouteUserChannelMessage(spaceID, content, parentMessageID string, attachments []msg.Attachment) ([]RoutingTarget, []RoutingNotice, error) {
	if r == nil || r.spaces == nil {
		return nil, nil, fmt.Errorf("router not configured")
	}
	mentions := ParseMentions(content, r.resolverFunc(), r.maxMentions)
	user := r.spaces.UserParticipant()
	parent := strings.TrimSpace(parentMessageID)
	draft := Message{
		AuthorID:        user.ID,
		AuthorKind:      ParticipantUser,
		Content:         content,
		Attachments:     attachments,
		Mentions:        mentions,
		ParentMessageID: parent,
	}

	// Resolve which agents this message wakes (explicit mentions, or listening
	// agents when there are none) BEFORE the append, so the wake intents can be
	// persisted in the SAME Space commit as the message. This is the durable
	// "append-before-claim" fact: a crash right after the user message is
	// written still leaves recoverable intents, since they can never land in a
	// later commit than the message itself.
	woken, kind, preNotice := r.resolveChannelTargets(spaceID, mentions, parent)

	resolveInfo := func(id string) PersonaInfo {
		if r.persona == nil {
			return PersonaInfo{ID: id}
		}
		if info, ok := r.persona(id); ok {
			return info
		}
		return PersonaInfo{ID: id}
	}
	// buildIntents is pure: it may only read the assigned message id and the
	// pre-append snapshot. The origin message is its own chain root (first
	// round), and the durable budget is DefaultRoutingBudget minus the intents
	// already persisted under that root (zero for a brand-new root).
	buildIntents := func(assignedID string, existing []Message) []RoutingIntent {
		if len(woken) == 0 {
			return nil
		}
		root := strings.TrimSpace(assignedID)
		spent := CountChainIntents(existing, root)
		remaining := DefaultRoutingBudget - spent
		if remaining <= 0 {
			return nil
		}
		intents := make([]RoutingIntent, 0, len(woken))
		for _, id := range woken {
			if len(intents) >= remaining {
				break
			}
			intents = append(intents, RoutingIntent{
				AgentID:         id,
				Kind:            kind,
				Reason:          firstRoundReason(kind),
				ParentMessageID: parent,
				ChainRoot:       root,
			})
		}
		return intents
	}
	written, _, err := r.spaces.AppendMessageWithIntents(spaceID, draft, woken, resolveInfo, buildIntents)
	if err != nil {
		return nil, nil, err
	}
	if preNotice != nil {
		preNotice.MessageID = written.ID
		return nil, []RoutingNotice{*preNotice}, nil
	}

	// The persisted intents on the written message are the wake authority; the
	// in-memory chain now only carries root/parent identity for reply routing
	// and the collaboration brief. Any woken mention that did not fit the
	// durable budget is reported as budget-exhausted.
	chain := r.chains.Start(written.ID, spaceID, DefaultRoutingBudget)
	chain.ParentMessageID = parent
	return r.fanOutIntents(chain, written, spaceID)
}

// resolveChannelTargets decides who a user channel message wakes and returns the
// intent kind ("mention"/"listen"), plus a pre-append notice when there is no
// deliverable target (ambiguous listening or nobody). The notice's MessageID is
// filled in by the caller once the message is written.
func (r *Router) resolveChannelTargets(spaceID string, mentions []string, parentMessageID string) ([]string, string, *RoutingNotice) {
	if len(mentions) > 0 {
		return mentions, "mention", nil
	}
	sp, _ := r.spaces.LoadSpace(spaceID)
	listening := ListeningAgents(sp, parentMessageID)
	switch {
	case len(listening) > 1:
		return nil, "", &RoutingNotice{Kind: NoticeListeningAmbiguous, SpaceID: spaceID, At: time.Now()}
	case len(listening) == 1:
		return listening, "listen", nil
	}
	return nil, "", &RoutingNotice{Kind: NoticeChannelNoTarget, SpaceID: spaceID, At: time.Now()}
}

// fanOutIntents turns the durable intents persisted on origin into live
// RoutingTargets (carrying the chain for reply routing), and emits
// budget-exhausted notices for mentions that were woken but did not fit the
// persisted budget.
func (r *Router) fanOutIntents(chain *RoutingChain, origin Message, spaceID string) ([]RoutingTarget, []RoutingNotice, error) {
	kept := make(map[string]bool, len(origin.RoutingIntents))
	wakes := make([]RoutingTarget, 0, len(origin.RoutingIntents))
	for _, it := range origin.RoutingIntents {
		kept[it.AgentID] = true
		chain.Spend(it.AgentID)
		wakes = append(wakes, RoutingTarget{AgentID: it.AgentID, OriginMessageID: origin.ID, Chain: chain, Reason: it.Reason})
	}
	var notices []RoutingNotice
	for _, id := range origin.Mentions {
		if !kept[id] {
			notices = append(notices, RoutingNotice{Kind: NoticeBudgetExhausted, SpaceID: spaceID, MessageID: origin.ID, AgentID: id, At: time.Now()})
		}
	}
	return wakes, notices, nil
}

func firstRoundReason(kind string) string {
	if kind == "listen" {
		return "called by listening"
	}
	return "called by mention"
}

// CountChainIntents counts persisted routing intents across messages whose
// ChainRoot matches root. It is the durable authority for routing-budget
// accounting, replacing the volatile in-memory ChainTracker: the budget spent
// on a chain is exactly the number of intents already committed under its root.
func CountChainIntents(messages []Message, root string) int {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0
	}
	n := 0
	for _, m := range messages {
		for _, it := range m.RoutingIntents {
			if strings.TrimSpace(it.ChainRoot) == root {
				n++
			}
		}
	}
	return n
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
		ok, why := chain.TrySpend(id)
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
		wakes = append(wakes, RoutingTarget{AgentID: id, OriginMessageID: originMessageID, Chain: chain})
	}
	return wakes, notices, nil
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
