package app

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
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
	// Identity of this projection, carried so Apply can validate a session's
	// ProjectionCheckpoint against the current (Space, Thread, Persona) view.
	SpaceID         string
	ParentMessageID string
	AgentID         string
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
	return ContextView{
		Messages:        runtimeContextMessages(candidates, in.AgentID),
		Profile:         profile,
		SpaceID:         strings.TrimSpace(in.SpaceID),
		ParentMessageID: strings.TrimSpace(in.ParentMessageID),
		AgentID:         strings.TrimSpace(in.AgentID),
	}
}

// Apply rebuilds the session runtime context from this projection. When the
// session carries a valid ProjectionCheckpoint for this exact view, the
// compacted prefix is replaced by the persistent summary and only the
// un-compacted Space suffix is replayed ([summary] + suffix); otherwise the
// full projection is loaded and any stale checkpoint is cleared. Either way the
// session is deterministically rebuilt from Space every round — no mutable
// runtime tail is preserved.
func (v ContextView) Apply(s *session.Session) {
	if s == nil {
		return
	}
	if cp, suffix, ok := v.resolveCheckpoint(s.Checkpoint); ok {
		wrapped := wrapCheckpointSummary(cp)
		s.Messages = nil
		s.Summary = wrapped
		s.Add(msg.Message{Role: "system", Content: "[Context Summary]\n" + wrapped})
		for _, m := range suffix {
			s.Add(m)
		}
		return
	}
	s.Checkpoint = nil
	s.Messages = nil
	s.Summary = strings.TrimSpace(v.Summary)
	for _, m := range v.Messages {
		s.Add(m)
	}
}

// resolveCheckpoint validates cp against this projection. It returns the
// checkpoint plus the Space suffix strictly after the compact boundary when the
// checkpoint is still applicable, or ok=false when it is missing or stale (so
// Apply falls back to a full rebuild).
func (v ContextView) resolveCheckpoint(cp *session.ProjectionCheckpoint) (*session.ProjectionCheckpoint, []msg.Message, bool) {
	if cp == nil || strings.TrimSpace(cp.Summary) == "" {
		return nil, nil, false
	}
	if cp.SpaceID != v.SpaceID || cp.ParentMessageID != v.ParentMessageID || cp.AgentID != v.AgentID {
		return nil, nil, false
	}
	if cp.Profile != string(v.Profile) {
		return nil, nil, false
	}
	idx := indexOfMessageID(v.Messages, cp.SummaryThroughMessageID)
	if idx < 0 {
		return nil, nil, false
	}
	if fingerprintMessages(v.Messages[:idx+1]) != cp.PrefixFingerprint {
		return nil, nil, false
	}
	return cp, v.Messages[idx+1:], true
}

func indexOfMessageID(msgs []msg.Message, id string) int {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1
	}
	for i, m := range msgs {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// wrapCheckpointSummary wraps the raw provider summary with provenance exactly
// once at projection time. cp.Summary is always stored raw, so this never
// re-wraps an already-wrapped summary (no provenance nesting). Zero times are
// passed so the wrapped text is deterministic across rounds (no time_range line
// that would otherwise churn the projection every turn).
func wrapCheckpointSummary(cp *session.ProjectionCheckpoint) string {
	return summaryWithProvenance(cp.Summary, summaryProvenance{
		Profile: ContextProfile(cp.Profile),
		SpaceID: cp.SpaceID,
	}, time.Time{}, time.Time{})
}

// fingerprintMessages hashes the compacted prefix by semantic content (ID,
// role, author, content, reasoning) in order. It deliberately excludes
// timestamps and counts so that an edit/delete/reorder of history invalidates
// the checkpoint (count alone would not change on an in-place edit).
func fingerprintMessages(msgs []msg.Message) string {
	h := sha1.New()
	for _, m := range msgs {
		io.WriteString(h, m.ID)
		h.Write([]byte{0x1f})
		io.WriteString(h, m.Role)
		h.Write([]byte{0x1f})
		io.WriteString(h, m.AgentID)
		h.Write([]byte{0x1f})
		io.WriteString(h, m.Content)
		h.Write([]byte{0x1f})
		io.WriteString(h, m.Reasoning)
		h.Write([]byte{0x1e})
	}
	return hex.EncodeToString(h.Sum(nil))
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
