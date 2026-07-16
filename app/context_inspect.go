package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

type ContextInspectInput struct {
	SpaceID          string
	Source           string
	SessionSource    string
	ParentMessageID  string
	AgentID          string
	ExcludeMessageID string
	Profile          ContextProfile
}

type ContextInspectMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	AuthorID  string    `json:"author_id,omitempty"`
	Content   string    `json:"content,omitempty"`
	Tokens    int       `json:"tokens,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ContextFilteredCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type ContextInspectView struct {
	Profile         ContextProfile          `json:"profile"`
	Source          string                  `json:"source,omitempty"`
	SessionSource   string                  `json:"session_source,omitempty"`
	SessionID       string                  `json:"session_id,omitempty"`
	SpaceID         string                  `json:"space_id,omitempty"`
	ParentMessageID string                  `json:"parent_message_id,omitempty"`
	AgentID         string                  `json:"agent_id,omitempty"`
	RawMessageCount int                     `json:"raw_message_count"`
	EligibleCount   int                     `json:"eligible_count"`
	SelectedCount   int                     `json:"selected_count"`
	FilteredCounts  []ContextFilteredCount  `json:"filtered_counts,omitempty"`
	SessionSummary  string                  `json:"session_summary,omitempty"`
	Messages        []ContextInspectMessage `json:"messages"`
	Notes           []string                `json:"notes,omitempty"`
}

type ContextResetInput struct {
	SpaceID         string
	Source          string
	SessionSource   string
	ParentMessageID string
	AgentID         string
	Action          string
}

type ContextResetResult struct {
	OK                     bool   `json:"ok"`
	Action                 string `json:"action"`
	Source                 string `json:"source,omitempty"`
	SessionSource          string `json:"session_source,omitempty"`
	PreviousSessionID      string `json:"previous_session_id,omitempty"`
	SessionID              string `json:"session_id,omitempty"`
	ClearedSummary         bool   `json:"cleared_summary,omitempty"`
	RemovedSummaryMessages int    `json:"removed_summary_messages,omitempty"`
	Note                   string `json:"note,omitempty"`
}

type ContextIdentity struct {
	SpaceID             string
	ThreadRootMessageID string
	AgentPersonaID      string
	TransportSource     string
	RuntimeSessionKey   string
}

func (a *App) InspectContext(in ContextInspectInput) (ContextInspectView, error) {
	if a == nil {
		return ContextInspectView{}, fmt.Errorf("app is nil")
	}
	identity, err := a.resolveContextIdentity(ContextInspectInput{
		SpaceID:         in.SpaceID,
		Source:          in.Source,
		SessionSource:   in.SessionSource,
		ParentMessageID: in.ParentMessageID,
		AgentID:         in.AgentID,
	})
	if err != nil {
		return ContextInspectView{}, err
	}
	in.SpaceID = identity.SpaceID
	in.Source = identity.TransportSource
	in.SessionSource = identity.RuntimeSessionKey
	in.ParentMessageID = strings.TrimSpace(in.ParentMessageID)
	in.AgentID = identity.AgentPersonaID
	view := ContextInspectView{
		Profile:         in.Profile,
		Source:          in.Source,
		SessionSource:   in.SessionSource,
		ParentMessageID: in.ParentMessageID,
		AgentID:         in.AgentID,
		Messages:        []ContextInspectMessage{},
	}
	if s, err := a.sessions.Current(in.SessionSource); err == nil && s != nil {
		view.SessionID = s.ID
		view.SessionSummary = strings.TrimSpace(s.Summary)
	}

	sp, err := a.inspectSpace(in)
	if err != nil {
		view.Notes = append(view.Notes, err.Error())
		return view, nil
	}
	if sp == nil {
		return a.inspectCurrentSession(view)
	}
	view.SpaceID = sp.ID
	view.Profile = contextProfile(ContextViewInput{
		Source:          in.Source,
		ParentMessageID: in.ParentMessageID,
		Profile:         in.Profile,
	}, sp)
	raw := contextMessages(sp, in.ParentMessageID)
	view.RawMessageCount = len(raw)
	candidates, filtered := inspectContextCandidates(raw, in.ExcludeMessageID, view.Profile)
	view.FilteredCounts = filtered
	view.EligibleCount = len(candidates)
	view.SelectedCount = len(candidates)
	view.Messages = inspectRuntimeMessages(candidates, in.AgentID)
	if sp.ID != "" {
		view.Notes = append(view.Notes, "Reset session starts a fresh runtime cache; it does not delete this Space history.")
	}
	return view, nil
}

func (a *App) ResetContext(in ContextResetInput) (ContextResetResult, error) {
	if a == nil {
		return ContextResetResult{}, fmt.Errorf("app is nil")
	}
	identity, err := a.resolveContextIdentity(ContextInspectInput{
		SpaceID:         in.SpaceID,
		Source:          in.Source,
		SessionSource:   in.SessionSource,
		ParentMessageID: in.ParentMessageID,
		AgentID:         in.AgentID,
	})
	if err != nil {
		return ContextResetResult{}, err
	}
	source := identity.TransportSource
	sessionSource := identity.RuntimeSessionKey
	action := strings.TrimSpace(strings.ToLower(in.Action))
	res := ContextResetResult{Action: action, Source: source, SessionSource: sessionSource, OK: true}
	current, _ := a.sessions.Current(sessionSource)
	if current != nil {
		res.PreviousSessionID = current.ID
	}
	switch action {
	case "runtime_session", "session", "reset-session":
		s, err := a.sessions.New(sessionSource)
		if err != nil {
			return ContextResetResult{}, err
		}
		res.Action = "runtime_session"
		res.SessionID = s.ID
		res.Note = "Started a fresh runtime session. Product Space/chat history is unchanged and may still seed future context."
		return res, nil
	case "summary", "reset-summary":
		if current == nil {
			var err error
			current, err = a.sessions.Current(sessionSource)
			if err != nil {
				return ContextResetResult{}, err
			}
		}
		res.Action = "summary"
		res.SessionID = current.ID
		res.ClearedSummary = strings.TrimSpace(current.Summary) != ""
		current.Summary = ""
		current.Messages, res.RemovedSummaryMessages = stripContextSummaryMessages(current.Messages)
		current.UpdatedAt = time.Now()
		if err := a.sessions.Save(current); err != nil {
			return ContextResetResult{}, err
		}
		res.Note = "Cleared compressed runtime summary only. Space/chat history is unchanged."
		return res, nil
	default:
		return ContextResetResult{}, fmt.Errorf("unknown context reset action: %s", in.Action)
	}
}

func (a *App) resolveContextIdentity(in ContextInspectInput) (ContextIdentity, error) {
	if a == nil {
		return ContextIdentity{}, fmt.Errorf("app is nil")
	}
	identity := ContextIdentity{
		SpaceID:             strings.TrimSpace(in.SpaceID),
		ThreadRootMessageID: strings.TrimSpace(in.ParentMessageID),
		AgentPersonaID:      strings.TrimSpace(in.AgentID),
		TransportSource:     strings.TrimSpace(in.Source),
		RuntimeSessionKey:   strings.TrimSpace(in.SessionSource),
	}
	if identity.SpaceID != "" {
		sp, err := a.spaces.LoadSpace(identity.SpaceID)
		if err != nil || sp == nil {
			return ContextIdentity{}, fmt.Errorf("space not found: %s", identity.SpaceID)
		}
		if identity.TransportSource == "" {
			identity.TransportSource = contextTransportSourceForSpace(sp)
		}
		if identity.AgentPersonaID == "" && sp.Kind == space.KindAgentDM {
			identity.AgentPersonaID = space.AgentParticipantID(sp)
		}
	} else if identity.TransportSource != "" {
		target := space.MapSource(identity.TransportSource)
		if target.Kind != "" {
			if space.IsSpaceID(target.Seed) {
				sp, err := a.spaces.LoadSpace(target.Seed)
				if err != nil || sp == nil {
					return ContextIdentity{}, fmt.Errorf("space not found: %s", target.Seed)
				}
				identity.SpaceID = sp.ID
				if identity.AgentPersonaID == "" && sp.Kind == space.KindAgentDM {
					identity.AgentPersonaID = space.AgentParticipantID(sp)
				}
			} else {
				sp, err := a.spaces.Resolve(identity.TransportSource, space.PersonaInfo{})
				if err != nil {
					return ContextIdentity{}, err
				}
				if sp != nil {
					identity.SpaceID = sp.ID
					if identity.AgentPersonaID == "" && sp.Kind == space.KindAgentDM {
						identity.AgentPersonaID = space.AgentParticipantID(sp)
					}
				}
			}
		}
	}
	if identity.RuntimeSessionKey == "" {
		identity.RuntimeSessionKey = contextRuntimeSessionKey(identity)
	}
	if identity.RuntimeSessionKey == "" {
		return ContextIdentity{}, fmt.Errorf("context identity requires a resolved space or explicit runtime session")
	}
	return identity, nil
}

func contextRuntimeSessionKey(identity ContextIdentity) string {
	if identity.SpaceID == "" {
		return ""
	}
	base := identity.SpaceID
	if identity.ThreadRootMessageID != "" {
		base += ":thread:" + identity.ThreadRootMessageID
	}
	if identity.AgentPersonaID != "" {
		base += ":persona:" + identity.AgentPersonaID
	}
	return base
}

func contextTransportSourceForSpace(sp *space.Space) string {
	if sp == nil {
		return ""
	}
	switch sp.Kind {
	case space.KindChannel:
		return "desktop:channel:" + sp.ID
	case space.KindAgentDM:
		return "desktop:agent:" + sp.ID
	case space.KindDirectChat:
		return "desktop:direct:" + sp.ID
	default:
		return ""
	}
}

func (a *App) runContextCommand(ctx context.Context, args []string) (string, error) {
	source := command.SourceFrom(ctx)
	sessionSource := command.SessionSourceFrom(ctx)
	if len(args) == 0 || args[0] == "inspect" {
		view, err := a.InspectContext(ContextInspectInput{
			Source:          source,
			SessionSource:   sessionSource,
			ParentMessageID: command.ParentMessageFrom(ctx),
			AgentID:         command.PersonaFrom(ctx),
		})
		if err != nil {
			return "", err
		}
		return contextInspectText(view), nil
	}
	switch args[0] {
	case "reset-session", "reset_session":
		res, err := a.ResetContext(ContextResetInput{
			Source:          source,
			SessionSource:   sessionSource,
			ParentMessageID: command.ParentMessageFrom(ctx),
			AgentID:         command.PersonaFrom(ctx),
			Action:          "runtime_session",
		})
		if err != nil {
			return "", err
		}
		return contextResetText(res), nil
	case "reset-summary", "reset_summary":
		res, err := a.ResetContext(ContextResetInput{
			Source:          source,
			SessionSource:   sessionSource,
			ParentMessageID: command.ParentMessageFrom(ctx),
			AgentID:         command.PersonaFrom(ctx),
			Action:          "summary",
		})
		if err != nil {
			return "", err
		}
		return contextResetText(res), nil
	default:
		return "usage: /context [inspect|reset-session|reset-summary]", nil
	}
}

func (a *App) inspectSpace(in ContextInspectInput) (*space.Space, error) {
	if a == nil || a.spaces == nil {
		return nil, nil
	}
	if id := strings.TrimSpace(in.SpaceID); id != "" {
		sp, err := a.spaces.LoadSpace(id)
		if err != nil || sp == nil {
			return nil, fmt.Errorf("space not found: %s", id)
		}
		return sp, nil
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		return nil, nil
	}
	target := space.MapSource(source)
	if target.Kind == "" {
		return nil, nil
	}
	if space.IsSpaceID(target.Seed) {
		sp, err := a.spaces.LoadSpace(target.Seed)
		if err != nil || sp == nil {
			return nil, fmt.Errorf("space not found: %s", target.Seed)
		}
		return sp, nil
	}
	sp, err := a.spaces.Resolve(source, space.PersonaInfo{})
	if err != nil {
		return nil, err
	}
	return sp, nil
}

func (a *App) inspectCurrentSession(view ContextInspectView) (ContextInspectView, error) {
	s, err := a.sessions.Current(view.SessionSource)
	if err != nil || s == nil {
		return view, err
	}
	view.SessionID = s.ID
	view.SessionSummary = strings.TrimSpace(s.Summary)
	view.RawMessageCount = len(s.Messages)
	filtered := map[string]int{}
	candidates := make([]msg.Message, 0, len(s.Messages))
	for _, m := range s.Messages {
		if !eligibleSessionSummaryMessage(m) {
			filtered["runtime_noise"]++
			continue
		}
		candidates = append(candidates, m)
	}
	view.FilteredCounts = sortedFilteredCounts(filtered)
	view.EligibleCount = len(candidates)
	view.SelectedCount = len(candidates)
	for _, m := range candidates {
		view.Messages = append(view.Messages, ContextInspectMessage{
			ID:        m.ID,
			Role:      m.Role,
			AuthorID:  m.AgentID,
			Content:   trimText(primaryText(m), 500),
			Tokens:    estimateMessage(m),
			CreatedAt: m.Timestamp,
		})
	}
	view.Notes = append(view.Notes, "No Space history was resolved; showing current runtime session only.")
	return view, nil
}

func inspectContextCandidates(raw []space.Message, excludeMessageID string, profile ContextProfile) ([]space.Message, []ContextFilteredCount) {
	excludeMessageID = strings.TrimSpace(excludeMessageID)
	filtered := map[string]int{}
	candidates := make([]space.Message, 0, len(raw))
	for _, m := range raw {
		if excludeMessageID != "" && m.ID == excludeMessageID {
			filtered["current_turn"]++
			continue
		}
		if reason := contextRejectReason(m, profile); reason != "" {
			filtered[reason]++
			continue
		}
		candidates = append(candidates, m)
	}
	return candidates, sortedFilteredCounts(filtered)
}

func sortedFilteredCounts(in map[string]int) []ContextFilteredCount {
	if len(in) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(in))
	for reason := range in {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	out := make([]ContextFilteredCount, 0, len(reasons))
	for _, reason := range reasons {
		out = append(out, ContextFilteredCount{Reason: reason, Count: in[reason]})
	}
	return out
}

func inspectRuntimeMessages(in []space.Message, agentID string) []ContextInspectMessage {
	out := make([]ContextInspectMessage, 0, len(in))
	for _, m := range in {
		rm := toRuntimeMessage(m, agentID)
		out = append(out, ContextInspectMessage{
			ID:        m.ID,
			Role:      rm.Role,
			AuthorID:  m.AuthorID,
			Content:   trimText(primaryText(rm), 500),
			Tokens:    estimateMessage(rm),
			CreatedAt: m.CreatedAt,
		})
	}
	return out
}

func stripContextSummaryMessages(in []msg.Message) ([]msg.Message, int) {
	out := make([]msg.Message, 0, len(in))
	removed := 0
	for _, m := range in {
		if m.Role == "system" && strings.HasPrefix(strings.TrimSpace(m.Content), "[Context Summary]") {
			removed++
			continue
		}
		out = append(out, m)
	}
	return out, removed
}

func contextInspectText(v ContextInspectView) string {
	var lines []string
	lines = append(lines, "Runtime context:")
	lines = append(lines, "  profile: "+string(v.Profile))
	if v.Source != "" {
		lines = append(lines, "  source: "+v.Source)
	}
	if v.SessionSource != "" {
		lines = append(lines, "  session_source: "+v.SessionSource)
	}
	if v.SessionID != "" {
		lines = append(lines, "  session_id: "+v.SessionID)
	}
	if v.SpaceID != "" {
		lines = append(lines, "  space_id: "+v.SpaceID)
	}
	if v.ParentMessageID != "" {
		lines = append(lines, "  parent_message_id: "+v.ParentMessageID)
	}
	lines = append(lines, fmt.Sprintf("  messages: raw=%d eligible=%d selected=%d", v.RawMessageCount, v.EligibleCount, v.SelectedCount))
	if len(v.FilteredCounts) > 0 {
		var parts []string
		for _, c := range v.FilteredCounts {
			parts = append(parts, fmt.Sprintf("%s=%d", c.Reason, c.Count))
		}
		lines = append(lines, "  filtered: "+strings.Join(parts, ", "))
	}
	if strings.TrimSpace(v.SessionSummary) != "" {
		lines = append(lines, "  session_summary: "+trimText(v.SessionSummary, 240))
	}
	if len(v.Messages) > 0 {
		lines = append(lines, "  selected:")
		for _, m := range v.Messages {
			lines = append(lines, fmt.Sprintf("    - %s %s %s", m.Role, shortID(m.ID), trimText(m.Content, 100)))
		}
	}
	if len(v.Notes) > 0 {
		lines = append(lines, "  notes: "+strings.Join(v.Notes, " | "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func contextResetText(r ContextResetResult) string {
	switch r.Action {
	case "runtime_session":
		return fmt.Sprintf("reset runtime session: %s -> %s\n%s", r.PreviousSessionID, r.SessionID, r.Note)
	case "summary":
		return fmt.Sprintf("reset summary: %s cleared=%t removed_summary_messages=%d\n%s", r.SessionID, r.ClearedSummary, r.RemovedSummaryMessages, r.Note)
	default:
		return r.Note
	}
}
