package cli

import (
	"strings"
	"time"

	"github.com/abcdlsj/sumi/space"
)

const noMentionHint = "No agent mentioned. Use @<persona> to wake one, or run sumi with --persona <id> to start a direct DM."

func (m *shellModel) personaResolver() space.DisplayResolver {
	if m == nil || m.app == nil || m.app.Personas() == nil {
		return nil
	}
	return space.DisplayResolverFunc(func(id string) string {
		if p := m.app.Personas().Get(id); p != nil {
			return p.Display
		}
		return ""
	})
}

func (m *shellModel) loadSpaceForCurrentSource() *space.Space {
	if m == nil || m.app == nil || m.app.Spaces() == nil {
		return nil
	}
	target := space.MapSource(m.source)
	if target.Kind == "" {
		return nil
	}
	sp, err := m.app.Spaces().EnsureForSource(m.source, space.PersonaInfo{ID: target.Seed})
	if err != nil {
		return nil
	}
	return sp
}

func (m *shellModel) loadSpaceTranscript() {
	m.spaceID = ""
	m.spaceMsgs = nil
	sp := m.loadSpaceForCurrentSource()
	if sp == nil {
		return
	}
	m.spaceID = sp.ID
	for _, line := range space.LinearTranscript(sp, m.personaResolver()) {
		m.appendTranscriptLine(line)
	}
}

func (m *shellModel) appendNewSpaceMessages() (added int, agents int) {
	sp := m.loadSpaceForCurrentSource()
	if sp == nil {
		return 0, 0
	}
	if m.spaceID == "" {
		m.spaceID = sp.ID
	}
	resolver := m.personaResolver()
	for _, line := range space.LinearTranscript(sp, resolver) {
		if m.spaceMsgs[line.MessageID] {
			continue
		}
		m.appendTranscriptLine(line)
		added++
		if line.AuthorKind == space.ParticipantAgent {
			agents++
		}
	}
	return added, agents
}

func (m *shellModel) appendTranscriptLine(line space.TranscriptLine) {
	if m.spaceMsgs == nil {
		m.spaceMsgs = map[string]bool{}
	}
	m.spaceMsgs[line.MessageID] = true
	item := chatItem{
		ID:   line.MessageID,
		Time: line.CreatedAt,
	}
	switch line.AuthorKind {
	case space.ParticipantUser:
		item.Kind = itemUser
		if strings.TrimSpace(line.Content) == "" {
			return
		}
		item.Segments = []chatSegment{{Kind: segText, Text: line.Content, Time: line.CreatedAt}}
	case space.ParticipantAgent:
		item.Kind = itemAssistant
		item.Author = line.Display
		if r := strings.TrimSpace(line.Reasoning); r != "" {
			item.Segments = append(item.Segments, chatSegment{Kind: segReasoning, Text: line.Reasoning, Time: line.CreatedAt})
		}
		if c := strings.TrimSpace(line.Content); c != "" {
			item.Segments = append(item.Segments, chatSegment{Kind: segText, Text: line.Content, Time: line.CreatedAt})
		}
		if len(item.Segments) == 0 {
			return
		}
	default:
		return
	}
	m.addItem(item)
}

func (m *shellModel) sourceTracksSpace() bool {
	if m == nil || m.app == nil || m.app.Spaces() == nil {
		return false
	}
	return space.MapSource(m.source).Kind != ""
}

func (m *shellModel) addNoMentionHintIfNeeded(addedAgents int) {
	if !space.SourceUsesRouter(m.source) {
		return
	}
	if space.HasLeadingMention(m.turnInput) {
		return
	}
	if addedAgents > 0 {
		return
	}
	m.addTextItem(itemNotice, noMentionHint, time.Now())
}
