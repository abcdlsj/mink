package application

import (
	"time"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	placementdomain "github.com/abcdlsj/sumi/internal/placement/domain"
)

type Placement struct {
	AgentID              string
	ComputerID           string
	AgentProfileRevision uint64
	AgentProfile         agentapp.Profile
	RuntimeSpec          agentapp.RuntimeSpec
	DesiredRevision      uint64
	State                placementdomain.State
	ErrorCode            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type SetCommand struct {
	RequestID  string
	Actor      authoritydomain.Principal
	AgentID    string
	ComputerID string
	Now        time.Time
}

type ComputerReadQuery struct {
	ComputerID      string
	RegistrationKey string
}

type AcknowledgeCommand struct {
	ComputerID      string
	RegistrationKey string
	AgentID         string
	DesiredRevision uint64
	State           placementdomain.State
	ErrorCode       string
	Now             time.Time
}
