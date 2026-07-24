package application

import (
	"crypto/sha256"
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type RuntimeSession struct {
	AgentID                  string
	ComputerID               string
	PlacementDesiredRevision uint64
	CreatedAt                time.Time
	ExpiresAt                time.Time
}

type RuntimeProof struct {
	tokenHash                [sha256.Size]byte
	agentID                  string
	computerID               string
	placementDesiredRevision uint64
}

func NewRuntimeProof(tokenHash [sha256.Size]byte, agentID, computerID string, placementDesiredRevision uint64) RuntimeProof {
	return RuntimeProof{
		tokenHash: tokenHash, agentID: agentID, computerID: computerID,
		placementDesiredRevision: placementDesiredRevision,
	}
}

func (proof RuntimeProof) Valid() bool {
	return proof.tokenHash != [sha256.Size]byte{} && proof.agentID != "" && proof.computerID != "" && proof.placementDesiredRevision > 0
}

func (proof RuntimeProof) TokenHash() [sha256.Size]byte {
	return proof.tokenHash
}

func (proof RuntimeProof) AgentID() string {
	return proof.agentID
}

func (proof RuntimeProof) ComputerID() string {
	return proof.computerID
}

func (proof RuntimeProof) PlacementDesiredRevision() uint64 {
	return proof.placementDesiredRevision
}

type RuntimeAuthentication struct {
	Principal authoritydomain.Principal
	Proof     RuntimeProof
}

type RunProof struct {
	RunID   string
	Attempt uint64
	Fence   uint64
}

func (proof RunProof) Valid() bool {
	return proof.RunID != "" && proof.Attempt > 0 && proof.Fence > 0
}

func (authentication RuntimeAuthentication) Valid() bool {
	return authentication.Principal.Kind == authoritydomain.PrincipalAgent &&
		authentication.Principal.ID != "" && authentication.Principal.OrganizationID != "" &&
		authentication.Proof.Valid() && authentication.Proof.AgentID() == authentication.Principal.ID
}

type CreateRuntimeSessionCommand struct {
	ComputerID               string
	RegistrationKey          string
	AgentID                  string
	PlacementDesiredRevision uint64
	Token                    string
	Now                      time.Time
	ExpiresAt                time.Time
}

type RenewRuntimeSessionCommand struct {
	Proof           RuntimeProof
	ComputerID      string
	RegistrationKey string
	Token           string
	Now             time.Time
	ExpiresAt       time.Time
}

type RevokeRuntimeSessionCommand struct {
	Proof           RuntimeProof
	ComputerID      string
	RegistrationKey string
	Now             time.Time
}

type CreateBrowserHandoffCommand struct {
	Human     authoritydomain.Principal
	Token     string
	Now       time.Time
	ExpiresAt time.Time
}

type ConsumeBrowserHandoffCommand struct {
	HandoffToken     string
	SessionToken     string
	Now              time.Time
	SessionExpiresAt time.Time
}

type AuthenticationIdentity struct {
	Provider string
	Subject  string
}

type PasswordDigest struct {
	Algorithm   string
	Salt        []byte
	Digest      []byte
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
}

type LocalAccount struct {
	Identity AuthenticationIdentity
	Human    authoritydomain.Principal
	Password PasswordDigest
}

type BindBootstrapLocalAccountCommand struct {
	RequestID        string
	BootstrapHuman   authoritydomain.Principal
	Identity         AuthenticationIdentity
	Password         PasswordDigest
	SessionToken     string
	Now              time.Time
	SessionExpiresAt time.Time
}

type CreateBrowserSessionCommand struct {
	Human     authoritydomain.Principal
	Token     string
	Now       time.Time
	ExpiresAt time.Time
}
