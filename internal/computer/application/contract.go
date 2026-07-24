package application

import (
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
)

type Computer struct {
	ID                  string
	Name                string
	OS                  computerdomain.OperatingSystem
	Arch                computerdomain.Architecture
	CreatedAt           time.Time
	LastSeenAt          time.Time
	CapabilityInventory computerdomain.CapabilityInventory
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
	RequestID           string
	PairingToken        string
	RegistrationKey     string
	Name                string
	OS                  computerdomain.OperatingSystem
	Arch                computerdomain.Architecture
	CapabilityInventory computerdomain.CapabilityInventory
	Now                 time.Time
}

type HeartbeatCommand struct {
	ComputerID          string
	RegistrationKey     string
	CapabilityInventory computerdomain.CapabilityInventory
	Now                 time.Time
}

type CredentialDeliveryState string

const (
	CredentialDeliveryQueued    CredentialDeliveryState = "queued"
	CredentialDeliveryClaimed   CredentialDeliveryState = "claimed"
	CredentialDeliverySucceeded CredentialDeliveryState = "succeeded"
	CredentialDeliveryFailed    CredentialDeliveryState = "failed"
	CredentialDeliveryExpired   CredentialDeliveryState = "expired"
)

type SealedCredential struct {
	Algorithm          string
	KeyID              string
	EphemeralPublicKey []byte
	Nonce              []byte
	Ciphertext         []byte
}

type CredentialDelivery struct {
	ID             string
	RequestID      string
	ComputerID     string
	AgentID        string
	CredentialKind string
	Sealed         SealedCredential
	State          CredentialDeliveryState
	BindingHandle  string
	ErrorCode      string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type EnqueueCredentialDeliveryCommand struct {
	RequestID      string
	Actor          authoritydomain.Principal
	ComputerID     string
	AgentID        string
	CredentialKind string
	Sealed         SealedCredential
	ExpiresAt      time.Time
	Now            time.Time
}

type ListCredentialDeliveriesQuery struct {
	Actor      authoritydomain.Principal
	ComputerID string
	AgentID    string
	Now        time.Time
}

type ClaimCredentialDeliveryCommand struct {
	ComputerID      string
	RegistrationKey string
	Now             time.Time
}

type CompleteCredentialDeliveryCommand struct {
	ComputerID      string
	RegistrationKey string
	DeliveryID      string
	BindingHandle   string
	ErrorCode       string
	Now             time.Time
}
