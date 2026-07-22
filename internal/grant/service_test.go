package grant

import (
	"testing"

	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

func TestCapabilityMapping(t *testing.T) {
	tests := []struct {
		proto  grantv1.Capability
		domain authoritydomain.Capability
	}{
		{grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN, authoritydomain.CapabilityOrganizationAdmin},
		{grantv1.Capability_CAPABILITY_HUMAN_CREATE, authoritydomain.CapabilityHumanCreate},
		{grantv1.Capability_CAPABILITY_GRANT_ISSUE, authoritydomain.CapabilityGrantIssue},
		{grantv1.Capability_CAPABILITY_GRANT_REVOKE, authoritydomain.CapabilityGrantRevoke},
		{grantv1.Capability_CAPABILITY_AUDIT_READ, authoritydomain.CapabilityAuditRead},
		{grantv1.Capability_CAPABILITY_AGENT_CREATE, authoritydomain.CapabilityAgentCreate},
		{grantv1.Capability_CAPABILITY_AGENT_PLACE, authoritydomain.CapabilityAgentPlace},
		{grantv1.Capability_CAPABILITY_SPACE_CREATE, authoritydomain.CapabilitySpaceCreate},
		{grantv1.Capability_CAPABILITY_SPACE_READ, authoritydomain.CapabilitySpaceRead},
		{grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE, authoritydomain.CapabilitySpaceMembers},
		{grantv1.Capability_CAPABILITY_SPACE_ARCHIVE, authoritydomain.CapabilitySpaceArchive},
		{grantv1.Capability_CAPABILITY_MESSAGE_SEND, authoritydomain.CapabilityMessageSend},
		{grantv1.Capability_CAPABILITY_RUN_EXECUTE, authoritydomain.CapabilityRunExecute},
		{grantv1.Capability_CAPABILITY_COMPUTER_PAIR, authoritydomain.CapabilityComputerPair},
	}
	for _, test := range tests {
		t.Run(string(test.domain), func(t *testing.T) {
			domain, ok := capabilityName(test.proto)
			if !ok || domain != test.domain {
				t.Fatalf("capabilityName(%v) = %q, %v", test.proto, domain, ok)
			}
			if proto := capabilityValue(test.domain); proto != test.proto {
				t.Fatalf("capabilityValue(%q) = %v", test.domain, proto)
			}
		})
	}
	if _, ok := capabilityName(grantv1.Capability_CAPABILITY_UNSPECIFIED); ok {
		t.Fatal("unspecified capability mapped to a domain capability")
	}
	if value := capabilityValue(authoritydomain.CapabilityWorkCreate); value != grantv1.Capability_CAPABILITY_UNSPECIFIED {
		t.Fatalf("internal capability leaked through proto mapping: %v", value)
	}
}
