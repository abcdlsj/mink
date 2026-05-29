package space

import "time"

// ParticipantKind discriminates who is in the space. Only "user"
// and "agent" exist in v1.
type ParticipantKind string

const (
	ParticipantUser  ParticipantKind = "user"
	ParticipantAgent ParticipantKind = "agent"
)

// ParticipantStatus mirrors the routing layer's view of an agent's
// availability. Per the proposal, the default is "available";
// "responding" flips while a wake-up is being handled; "offline"
// is reserved for adapters that can disconnect.
type ParticipantStatus string

const (
	StatusAvailable  ParticipantStatus = "available"
	StatusResponding ParticipantStatus = "responding"
	StatusOffline    ParticipantStatus = "offline"
)

// Participant is a member of a Space. The set is the source of
// truth for the right-rail roster; nothing else infers membership
// from event history.
type Participant struct {
	ID       string            `json:"id"`
	Kind     ParticipantKind   `json:"kind"`
	Display  string            `json:"display,omitempty"`
	Role     string            `json:"role,omitempty"`
	Status   ParticipantStatus `json:"status"`
	JoinedAt time.Time         `json:"joined_at"`
}
