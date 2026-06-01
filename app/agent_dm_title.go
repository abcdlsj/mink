package app

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/space"
)

// MaxAgentDMTitleLen is the cap for auto-generated AgentDM titles.
// Iris's spec: short, like a conversation handle, not a summary.
const MaxAgentDMTitleLen = 32

// MinAgentDMTitleSeedLen is the minimum trimmed length the first
// user message must have before we'll auto-derive a title from it.
// Anything shorter is presumed to be a placeholder ("hi", "在吗",
// etc.) and we leave the Space title empty so the UI keeps showing
// "New chat" until the next turn.
const MinAgentDMTitleSeedLen = 6

// MaybeAutoTitleAgentDM is called once per AgentDM Space, after the
// first agent reply has landed. Iris's three constraints:
//   - never block the reply
//   - lock the title once a substantive one is generated
//   - title is a navigation label, not a summary
//
// This is the rule-based path: derive the title from the first user
// message via a short, deterministic truncation. The path is safe to
// run on the hot turn flow because it does not call out to a runtime.
// LLM-generated titles can replace this in a follow-on phase.
func (a *App) MaybeAutoTitleAgentDM(spaceID string) {
	if a == nil || a.spaces == nil || a.bus == nil {
		return
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil {
		return
	}
	if sp.Kind != space.KindAgentDM {
		return
	}
	if !shouldAutoTitleAgentDM(sp) {
		return
	}
	seed := firstSubstantiveUserMessageContent(sp)
	if seed == "" {
		return
	}
	title := deriveAgentDMTitle(seed)
	if strings.TrimSpace(title) == "" {
		return
	}
	if err := a.spaces.UpdateTitle(sp.ID, title); err != nil {
		return
	}
	a.bus.Publish(bus.Event{
		Type:    bus.SpaceTitleChanged,
		SpaceID: sp.ID,
		Text:    title,
	})
}

// shouldAutoTitleAgentDM returns true when the Space is a
// multi-instance AgentDM that has never had a meaningful title set.
// Once the title is something other than the machine seed or empty,
// we lock and never overwrite (Iris's no-flapping rule).
func shouldAutoTitleAgentDM(sp *space.Space) bool {
	t := strings.TrimSpace(sp.Title)
	if t == "" {
		return true
	}
	personaID := agentParticipantOnlyID(sp)
	return looksLikeAgentDMMachineSeed(t, personaID)
}

func agentParticipantOnlyID(sp *space.Space) string {
	for _, p := range sp.Participants {
		if p.Kind == space.ParticipantAgent {
			return p.ID
		}
	}
	return ""
}

// looksLikeAgentDMMachineSeed mirrors the desktop backend's
// isAgentDMMachineSeed. We duplicate it here rather than reach into
// the desktop package; the desktop projection is one consumer of the
// title, but Space.Title is owned by the app layer.
func looksLikeAgentDMMachineSeed(t, personaID string) bool {
	if personaID == "" {
		return false
	}
	prefix := personaID + "-"
	if !strings.HasPrefix(t, prefix) {
		return false
	}
	tail := t[len(prefix):]
	if len(tail) != 8 {
		return false
	}
	for _, r := range tail {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func firstUserMessageContent(sp *space.Space) string {
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser && strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}

// firstSubstantiveUserMessageContent walks every user message in
// sp.Messages and returns the first one that passes looksSubstantive.
// This guards Iris's "等下一次有信息量回合再生成" rule: if the
// conversation opened with `hi` / `在吗`, we keep waiting; once the
// user types something specific later, that becomes the title seed.
func firstSubstantiveUserMessageContent(sp *space.Space) string {
	for _, m := range sp.Messages {
		if m.AuthorKind != space.ParticipantUser {
			continue
		}
		if !looksSubstantive(m.Content) {
			continue
		}
		return m.Content
	}
	return ""
}

// looksSubstantive applies Iris's "no garbage title" filter: skip
// the auto-title if the first user message is a placeholder /
// greeting / vague open. Length threshold + a small denylist.
func looksSubstantive(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	if utf8.RuneCountInString(trimmed) < MinAgentDMTitleSeedLen {
		return false
	}
	lowered := strings.ToLower(trimmed)
	for _, banned := range []string{
		"hi", "hey", "hello",
		"yo", "ok", "okay", "thanks", "thank you",
		"在吗", "你好", "您好", "嗨",
		"帮我看看", "看一下", "看下",
	} {
		if lowered == banned {
			return false
		}
	}
	return true
}

// deriveAgentDMTitle maps a substantive first user message to a
// short navigation label. Strategy:
//   - take the first sentence (split on . ? ! ; 。 ？ ！ 　)
//   - collapse whitespace
//   - cap at MaxAgentDMTitleLen runes; ellipsis if over
//   - strip trailing terminal punctuation
//
// No LLM involvement; deterministic and synchronous.
func deriveAgentDMTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	first := s
	if i := indexAnyRune(s, ".!?;。？！"); i > 0 {
		first = s[:i]
	}
	first = collapseWhitespace(first)
	first = strings.TrimRightFunc(first, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == ';' || r == ',' || r == '。' || r == '？' || r == '！' || r == '；' || r == '，' || unicode.IsSpace(r)
	})
	rl := []rune(first)
	if len(rl) > MaxAgentDMTitleLen {
		return string(rl[:MaxAgentDMTitleLen]) + "…"
	}
	return first
}

func indexAnyRune(s, chars string) int {
	for i, r := range s {
		for _, c := range chars {
			if r == c {
				return i
			}
		}
	}
	return -1
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace && b.Len() > 0 {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
