package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

type ContextProfile string

const (
	ContextProfileDirect   ContextProfile = "direct"
	ContextProfileAgentDM  ContextProfile = "agent_dm"
	ContextProfileChannel  ContextProfile = "channel"
	ContextProfileThread   ContextProfile = "thread"
	ContextProfileTelegram ContextProfile = "telegram"
	ContextProfileCLI      ContextProfile = "cli"
)

type ContextView struct {
	Messages []msg.Message
	Summary  string
	Profile  ContextProfile
}

type ContextViewInput struct {
	SpaceID          string
	Source           string
	ParentMessageID  string
	AgentID          string
	ExcludeMessageID string
	Profile          ContextProfile
}

func (a *App) BuildContextView(in ContextViewInput) ContextView {
	if a == nil || a.spaces == nil {
		return ContextView{}
	}
	sp, err := a.spaces.LoadSpace(strings.TrimSpace(in.SpaceID))
	if err != nil || sp == nil {
		return ContextView{}
	}
	profile := contextProfile(in, sp)
	raw := contextMessages(sp, in.ParentMessageID)
	candidates := filterContextMessages(raw, in.ExcludeMessageID, profile)
	return ContextView{Messages: runtimeContextMessages(candidates, in.AgentID), Profile: profile}
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

func contextProfile(in ContextViewInput, sp *space.Space) ContextProfile {
	if in.Profile != "" {
		return in.Profile
	}
	source := strings.TrimSpace(in.Source)
	switch {
	case strings.HasPrefix(source, "tg:"):
		return ContextProfileTelegram
	case strings.HasPrefix(source, "cli:"):
		if strings.TrimSpace(in.ParentMessageID) != "" {
			return ContextProfileThread
		}
		return ContextProfileCLI
	case strings.TrimSpace(in.ParentMessageID) != "":
		return ContextProfileThread
	}
	if sp != nil {
		switch sp.Kind {
		case space.KindAgentDM:
			return ContextProfileAgentDM
		case space.KindChannel:
			return ContextProfileChannel
		case space.KindDirectChat:
			return ContextProfileDirect
		}
	}
	return ContextProfileDirect
}

type summaryProvenance struct {
	Profile         ContextProfile
	Source          string
	SpaceID         string
	ParentMessageID string
	MessageCount    int
}

func summaryWithProvenance(summary string, p summaryProvenance, start, end time.Time) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	var lines []string
	lines = append(lines, "Historical summary (weak context; prefer current turn and recent messages for facts).")
	if p.Profile != "" {
		lines = append(lines, "profile="+string(p.Profile))
	}
	if s := strings.TrimSpace(p.Source); s != "" {
		lines = append(lines, "source="+s)
	}
	if s := strings.TrimSpace(p.SpaceID); s != "" {
		lines = append(lines, "space_id="+s)
	}
	if s := strings.TrimSpace(p.ParentMessageID); s != "" {
		lines = append(lines, "parent_message_id="+s)
	}
	if p.MessageCount > 0 {
		lines = append(lines, fmt.Sprintf("message_count=%d", p.MessageCount))
	}
	if !start.IsZero() || !end.IsZero() {
		lines = append(lines, "time_range="+formatTimeRange(start, end))
	}
	lines = append(lines, "", summary)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatTimeRange(start, end time.Time) string {
	if start.IsZero() && end.IsZero() {
		return "unknown"
	}
	if start.IsZero() {
		start = end
	}
	if end.IsZero() {
		end = start
	}
	return start.UTC().Format(time.RFC3339) + ".." + end.UTC().Format(time.RFC3339)
}
