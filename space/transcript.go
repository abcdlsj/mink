package space

import "time"

type TranscriptLine struct {
	MessageID  string
	AuthorID   string
	AuthorKind ParticipantKind
	Display    string
	Content    string
	Reasoning  string
	CreatedAt  time.Time
}

func LinearTranscript(sp *Space, resolver DisplayResolver) []TranscriptLine {
	if sp == nil {
		return nil
	}
	out := make([]TranscriptLine, 0, len(sp.Messages))
	for _, m := range sp.Messages {
		out = append(out, TranscriptLine{
			MessageID:  m.ID,
			AuthorID:   m.AuthorID,
			AuthorKind: m.AuthorKind,
			Display:    MessageAuthorDisplay(sp, m, resolver),
			Content:    m.Content,
			Reasoning:  m.Reasoning,
			CreatedAt:  m.CreatedAt,
		})
	}
	return out
}
