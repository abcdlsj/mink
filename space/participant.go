package space

import "time"

type ParticipantKind string

const (
	ParticipantUser   ParticipantKind = "user"
	ParticipantAgent  ParticipantKind = "agent"
	ParticipantSystem ParticipantKind = "system"
)

type ParticipantStatus string

const (
	StatusAvailable  ParticipantStatus = "available"
	StatusResponding ParticipantStatus = "responding"
	StatusOffline    ParticipantStatus = "offline"
)

type Participant struct {
	ID       string            `json:"id"`
	Kind     ParticipantKind   `json:"kind"`
	Display  string            `json:"display,omitempty"`
	Role     string            `json:"role,omitempty"`
	Status   ParticipantStatus `json:"status"`
	JoinedAt time.Time         `json:"joined_at"`
}
