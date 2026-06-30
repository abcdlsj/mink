package app

import (
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/space"
)

type ContextPackInput struct {
	SpaceID         string
	ParentMessageID string
	AgentID         string
	TaskID          string
}

type ContextPack struct {
	SpaceID         string               `json:"space_id,omitempty"`
	ParentMessageID string               `json:"parent_message_id,omitempty"`
	AgentID         string               `json:"agent_id,omitempty"`
	TaskID          string               `json:"task_id,omitempty"`
	Segments        []ContextPackSegment `json:"segments,omitempty"`
}

type ContextPackSegment struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	RefID   string `json:"ref_id,omitempty"`
}

func (a *App) BuildContextPack(in ContextPackInput) ContextPack {
	pack := ContextPack{
		SpaceID:         strings.TrimSpace(in.SpaceID),
		ParentMessageID: strings.TrimSpace(in.ParentMessageID),
		AgentID:         strings.TrimSpace(in.AgentID),
		TaskID:          strings.TrimSpace(in.TaskID),
	}
	if a == nil || a.spaces == nil || pack.SpaceID == "" {
		return pack
	}
	sp, err := a.spaces.LoadSpace(pack.SpaceID)
	if err != nil || sp == nil {
		return pack
	}

	if seg, ok := a.contextPackTriggerSegment(sp, pack.ParentMessageID); ok {
		pack.Segments = append(pack.Segments, seg)
	}
	if seg, ok := a.contextPackScopeSummarySegment(sp, pack.ParentMessageID, pack.AgentID); ok {
		pack.Segments = append(pack.Segments, seg)
	}
	pack.Segments = append(pack.Segments, a.contextPackMemoryRefSegments(in)...)
	return pack
}

func (a *App) contextPackTriggerSegment(sp *space.Space, parentMessageID string) (ContextPackSegment, bool) {
	if sp == nil {
		return ContextPackSegment{}, false
	}
	msgs := contextMessages(sp, parentMessageID)
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.AuthorKind != space.ParticipantUser || strings.TrimSpace(m.Content) == "" {
			continue
		}
		author := strings.TrimSpace(m.AuthorID)
		if author == "" {
			author = "user"
		}
		return ContextPackSegment{
			Kind:    "trigger",
			Title:   "Trigger",
			Summary: author + ": " + trimText(m.Content, 220),
			RefID:   m.ID,
		}, true
	}
	return ContextPackSegment{}, false
}

func (a *App) contextPackScopeSummarySegment(sp *space.Space, parentMessageID, agentID string) (ContextPackSegment, bool) {
	if sp == nil {
		return ContextPackSegment{}, false
	}
	raw := contextMessages(sp, parentMessageID)
	candidates := filterContextMessages(raw, "", contextProfile(ContextViewInput{
		SpaceID:         sp.ID,
		ParentMessageID: parentMessageID,
		AgentID:         agentID,
	}, sp))
	if len(candidates) == 0 {
		return ContextPackSegment{}, false
	}
	summary := wakeContextSummary(candidates, agentID, summaryProvenance{
		Profile:         contextProfile(ContextViewInput{ParentMessageID: parentMessageID}, sp),
		SpaceID:         sp.ID,
		ParentMessageID: parentMessageID,
		MessageCount:    len(candidates),
	})
	if strings.TrimSpace(summary) == "" {
		return ContextPackSegment{}, false
	}
	title := "Scope Summary"
	if strings.TrimSpace(parentMessageID) != "" {
		title = "Thread Summary"
	}
	return ContextPackSegment{
		Kind:    "scope_summary",
		Title:   title,
		Summary: summary,
		RefID:   firstNonEmpty(strings.TrimSpace(parentMessageID), sp.ID),
	}, true
}

func (a *App) contextPackMemoryRefSegments(in ContextPackInput) []ContextPackSegment {
	if a == nil {
		return nil
	}
	scopes := []struct {
		kind string
		key  string
	}{
		{kind: "channel", key: strings.TrimSpace(in.SpaceID)},
		{kind: "persona", key: strings.TrimSpace(in.AgentID)},
		{kind: "workspace", key: strings.TrimSpace(a.cfg.Workspace)},
	}
	var out []ContextPackSegment
	seen := map[string]bool{}
	for _, sc := range scopes {
		if sc.kind == "" {
			continue
		}
		scopeID := sc.kind + "\x00" + sc.key
		if seen[scopeID] {
			continue
		}
		seen[scopeID] = true
		for _, doc := range a.recentMemoryDocs(sc.kind, sc.key, 2) {
			if strings.TrimSpace(doc.ID) == "" {
				continue
			}
			out = append(out, ContextPackSegment{
				Kind:    "memory_ref",
				Title:   fmt.Sprintf("Memory: %s", strings.TrimSpace(doc.Title)),
				Summary: strings.TrimSpace(doc.Summary),
				RefID:   memoryRefID(sc.kind, sc.key, doc.ID),
			})
		}
	}
	return out
}

func memoryRefID(kind, key, id string) string {
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	id = strings.TrimSpace(id)
	if key == "" {
		return kind + ":" + id
	}
	return kind + ":" + key + ":" + id
}
