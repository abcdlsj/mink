package application

import (
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
)

type Computer struct {
	ID                         string
	Name                       string
	OS                         computerdomain.OperatingSystem
	Arch                       computerdomain.Architecture
	CreatedAt                  time.Time
	LastSeenAt                 time.Time
	SandboxCapability          computerdomain.SandboxCapability
	SandboxDeclarationRevision uint64
}

type Pairing struct {
	ID        string
	ExpiresAt time.Time
}

type PreparePairingCommand struct {
	RequestID string
	Actor     authoritydomain.Principal
	Token     string
	ExpiresAt time.Time
	Now       time.Time
}

type PairCommand struct {
	RequestID         string
	PairingToken      string
	RegistrationKey   string
	Name              string
	OS                computerdomain.OperatingSystem
	Arch              computerdomain.Architecture
	SandboxCapability computerdomain.SandboxCapability
	Now               time.Time
}

type RegistrationCommand struct {
	RegistrationKey   string
	Name              string
	OS                computerdomain.OperatingSystem
	Arch              computerdomain.Architecture
	SandboxCapability computerdomain.SandboxCapability
	Now               time.Time
}

type HeartbeatCommand struct {
	ComputerID        string
	RegistrationKey   string
	SandboxCapability computerdomain.SandboxCapability
	Now               time.Time
}
