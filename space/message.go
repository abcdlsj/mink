package space

import "time"

// Message is one entry in a Space's timeline. Per the proposal:
//   - every visible message has a real AuthorID + AuthorKind
//   - ParentMessageID points at another message in the SAME space
//     and turns this message into a thread reply
//   - Mentions captures @-targets after the routing layer parses
//     the content; v1 uses these to drive wake-up + atomic
//     participant insertion.
//
// Events / reasoning / tool-call payloads remain in
// `plugins/desktop/types.go` for now; once P3 desktop migrates
// to read Space directly, the rendering struct will move next to
// this file.
type Message struct {
	ID              string    `json:"id"`
	SpaceID         string    `json:"space_id"`
	ParentMessageID string    `json:"parent_message_id,omitempty"`
	AuthorID        string    `json:"author_id"`
	AuthorKind      ParticipantKind `json:"author_kind"`
	Content         string    `json:"content,omitempty"`
	Reasoning       string    `json:"reasoning,omitempty"`
	Mentions        []string  `json:"mentions,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}
