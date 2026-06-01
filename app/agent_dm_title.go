package app

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/space"
)

const (
	maxTitleLen     = 32
	minTitleSeedLen = 6
)

var titleSkip = map[string]bool{
	"hi": true, "hey": true, "hello": true, "yo": true,
	"ok": true, "okay": true, "thanks": true, "thank you": true,
	"在吗": true, "你好": true, "您好": true, "嗨": true,
	"帮我看看": true, "看一下": true, "看下": true,
}

func (a *App) MaybeAutoTitleAgentDM(spaceID string) {
	if a == nil || a.spaces == nil || a.bus == nil {
		return
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil || sp.Kind != space.KindAgentDM {
		return
	}
	if !needsAutoTitle(sp) {
		return
	}
	seed := firstSubstantive(sp)
	if seed == "" {
		return
	}
	title := deriveTitle(seed)
	if title == "" {
		return
	}
	if err := a.spaces.UpdateTitle(sp.ID, title); err != nil {
		return
	}
	a.bus.Publish(bus.Event{Type: bus.SpaceTitleChanged, SpaceID: sp.ID, Text: title})
}

func needsAutoTitle(sp *space.Space) bool {
	t := strings.TrimSpace(sp.Title)
	if t == "" {
		return true
	}
	return isMachineSeed(t, agentID(sp))
}

func agentID(sp *space.Space) string {
	for _, p := range sp.Participants {
		if p.Kind == space.ParticipantAgent {
			return p.ID
		}
	}
	return ""
}

func isMachineSeed(t, persona string) bool {
	if persona == "" || !strings.HasPrefix(t, persona+"-") {
		return false
	}
	tail := t[len(persona)+1:]
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

func firstSubstantive(sp *space.Space) string {
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser && substantive(m.Content) {
			return m.Content
		}
	}
	return ""
}

func substantive(s string) bool {
	t := strings.TrimSpace(s)
	if utf8.RuneCountInString(t) < minTitleSeedLen {
		return false
	}
	return !titleSkip[strings.ToLower(t)]
}

func deriveTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := indexAnyRune(s, ".!?;。？！"); i > 0 {
		s = s[:i]
	}
	s = collapse(s)
	s = strings.TrimRightFunc(s, isTrailing)
	rl := []rune(s)
	if len(rl) > maxTitleLen {
		return string(rl[:maxTitleLen]) + "…"
	}
	return s
}

func isTrailing(r rune) bool {
	switch r {
	case '.', '!', '?', ';', ',', '。', '？', '！', '；', '，':
		return true
	}
	return unicode.IsSpace(r)
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

func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	return strings.TrimSpace(b.String())
}
