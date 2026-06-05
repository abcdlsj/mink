package app

import (
	"strings"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

type ContextView struct {
	Messages []msg.Message
	Summary  string
}

type ContextViewInput struct {
	SpaceID          string
	ParentMessageID  string
	AgentID          string
	ExcludeMessageID string
	TokenLimit       int
}

func (a *App) BuildContextView(in ContextViewInput) ContextView {
	if a == nil || a.spaces == nil {
		return ContextView{}
	}
	sp, err := a.spaces.LoadSpace(strings.TrimSpace(in.SpaceID))
	if err != nil || sp == nil {
		return ContextView{}
	}
	limit := in.TokenLimit
	if limit <= 0 {
		limit = a.wakeContextTokenLimit()
	}
	candidates := filterContextMessages(contextMessages(sp, in.ParentMessageID), in.ExcludeMessageID)
	kept := boundedContextMessages(candidates, in.AgentID, limit)
	view := ContextView{Messages: runtimeContextMessages(kept, in.AgentID)}
	if dropped := len(candidates) - len(kept); dropped > 0 {
		view.Summary = wakeContextSummary(candidates[:dropped], in.AgentID)
	}
	return view
}

func (v ContextView) Apply(s *session.Session) {
	if s == nil {
		return
	}
	s.Messages = nil
	s.Summary = strings.TrimSpace(v.Summary)
	for _, m := range v.Messages {
		s.Add(m)
	}
}

func runtimeContextMessages(in []space.Message, agentID string) []msg.Message {
	out := make([]msg.Message, 0, len(in))
	for _, m := range in {
		out = append(out, toRuntimeMessage(m, agentID))
	}
	return out
}
