package telegram

import (
	"strings"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/space"
	tele "gopkg.in/telebot.v4"
)

func spaceSnapshotIDs(a *app.App, src string) map[string]bool {
	if a == nil || a.Spaces() == nil {
		return nil
	}
	target := space.MapSource(src)
	if target.Kind == "" {
		return nil
	}
	sp, err := a.Spaces().EnsureForSource(src, space.PersonaInfo{ID: target.Seed})
	if err != nil || sp == nil {
		return nil
	}
	seen := make(map[string]bool, len(sp.Messages))
	for _, m := range sp.Messages {
		seen[m.ID] = true
	}
	return seen
}

func newSpaceAgentReplies(a *app.App, src string, before map[string]bool) []string {
	if a == nil || a.Spaces() == nil {
		return nil
	}
	target := space.MapSource(src)
	if target.Kind == "" {
		return nil
	}
	sp, err := a.Spaces().EnsureForSource(src, space.PersonaInfo{ID: target.Seed})
	if err != nil || sp == nil {
		return nil
	}
	resolver := personaResolver(a)
	var out []string
	for _, line := range space.LinearTranscript(sp, resolver) {
		if before[line.MessageID] {
			continue
		}
		if line.AuthorKind != space.ParticipantAgent {
			continue
		}
		if formatted := formatAgentMessage(line); formatted != "" {
			out = append(out, formatted)
		}
	}
	return out
}

func formatAgentMessage(line space.TranscriptLine) string {
	display := strings.TrimSpace(line.Display)
	body := strings.TrimSpace(line.Content)
	if body == "" {
		return ""
	}
	if display == "" {
		return body
	}
	return display + ": " + body
}

func personaResolver(a *app.App) space.DisplayResolver {
	if a == nil || a.Personas() == nil {
		return nil
	}
	return space.DisplayResolverFunc(func(id string) string {
		if p := a.Personas().Get(id); p != nil {
			return p.Display
		}
		return ""
	})
}

func sourceTracksSpace(src string) bool {
	return space.MapSource(src).Kind != ""
}

func noMentionHintForSource(src, input string, agents int) string {
	if !space.SourceUsesRouter(src) {
		return ""
	}
	if space.HasLeadingMention(input) {
		return ""
	}
	if agents > 0 {
		return ""
	}
	return "No agent mentioned. Use @<persona> in your message to wake one."
}

func sendSpaceReplies(bot *tele.Bot, c tele.Context, a *app.App, src, input string, before map[string]bool) error {
	replies := newSpaceAgentReplies(a, src, before)
	for _, reply := range replies {
		if err := sendOutput(bot, c, reply); err != nil {
			return err
		}
	}
	if hint := noMentionHintForSource(src, input, len(replies)); hint != "" {
		return c.Send(hint)
	}
	return nil
}
