package application

import (
	"crypto/sha256"
	"testing"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

func TestRuntimeAuthenticationValid(t *testing.T) {
	hash := sha256.Sum256([]byte("runtime-token"))
	proof := NewRuntimeProof(hash, "agent-id", "computer-id", 3)
	tests := []struct {
		name           string
		authentication RuntimeAuthentication
		valid          bool
	}{
		{
			name: "current agent binding",
			authentication: RuntimeAuthentication{
				Principal: authoritydomain.Principal{Kind: authoritydomain.PrincipalAgent, ID: "agent-id", OrganizationID: "organization-id"},
				Proof:     proof,
			},
			valid: true,
		},
		{
			name: "different agent",
			authentication: RuntimeAuthentication{
				Principal: authoritydomain.Principal{Kind: authoritydomain.PrincipalAgent, ID: "other-agent-id", OrganizationID: "organization-id"},
				Proof:     proof,
			},
		},
		{
			name: "human principal",
			authentication: RuntimeAuthentication{
				Principal: authoritydomain.Principal{Kind: authoritydomain.PrincipalHuman, ID: "agent-id", OrganizationID: "organization-id"},
				Proof:     proof,
			},
		},
		{
			name: "empty proof",
			authentication: RuntimeAuthentication{
				Principal: authoritydomain.Principal{Kind: authoritydomain.PrincipalAgent, ID: "agent-id", OrganizationID: "organization-id"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.authentication.Valid(); got != test.valid {
				t.Fatalf("Valid() = %t, want %t", got, test.valid)
			}
		})
	}
}

func TestRuntimeProofAccessors(t *testing.T) {
	hash := sha256.Sum256([]byte("runtime-token"))
	proof := NewRuntimeProof(hash, "agent-id", "computer-id", 3)
	if !proof.Valid() || proof.TokenHash() != hash || proof.AgentID() != "agent-id" || proof.ComputerID() != "computer-id" || proof.PlacementGeneration() != 3 {
		t.Fatal("runtime proof did not preserve its binding")
	}
}
