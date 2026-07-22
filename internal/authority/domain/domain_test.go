package domain

import "testing"

func TestPrincipalValid(t *testing.T) {
	tests := []struct {
		name      string
		principal Principal
		want      bool
	}{
		{"system", Principal{Kind: PrincipalSystem, OrganizationID: "organization"}, true},
		{"system with id", Principal{Kind: PrincipalSystem, ID: "system", OrganizationID: "organization"}, false},
		{"human", Principal{Kind: PrincipalHuman, ID: "human", OrganizationID: "organization"}, true},
		{"agent", Principal{Kind: PrincipalAgent, ID: "agent", OrganizationID: "organization"}, true},
		{"missing organization", Principal{Kind: PrincipalHuman, ID: "human"}, false},
		{"missing principal id", Principal{Kind: PrincipalAgent, OrganizationID: "organization"}, false},
		{"unknown kind", Principal{Kind: "unknown", ID: "principal", OrganizationID: "organization"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.principal.Valid(); got != test.want {
				t.Fatalf("Valid() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestScopeValid(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		want  bool
	}{
		{"organization", Scope{Kind: ScopeOrganization, ID: "organization"}, true},
		{"agent", Scope{Kind: ScopeAgent, ID: "agent"}, true},
		{"computer", Scope{Kind: ScopeComputer, ID: "computer"}, true},
		{"space", Scope{Kind: ScopeSpace, ID: "space"}, true},
		{"work", Scope{Kind: ScopeWork, ID: "work"}, true},
		{"missing id", Scope{Kind: ScopeSpace}, false},
		{"unknown kind", Scope{Kind: "unknown", ID: "scope"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.scope.Valid(); got != test.want {
				t.Fatalf("Valid() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCapabilityAllowsScope(t *testing.T) {
	tests := []struct {
		name       string
		capability Capability
		scope      ScopeKind
		want       bool
	}{
		{"work create organization", CapabilityWorkCreate, ScopeOrganization, true},
		{"work create work", CapabilityWorkCreate, ScopeWork, false},
		{"work read organization", CapabilityWorkRead, ScopeOrganization, true},
		{"work read work", CapabilityWorkRead, ScopeWork, true},
		{"work read space", CapabilityWorkRead, ScopeSpace, false},
		{"computer pair existing scope", CapabilityComputerPair, ScopeWork, true},
		{"unknown capability", "unknown", ScopeOrganization, false},
		{"unrestricted capability defers scope validation", CapabilityAgentCreate, "unknown", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.capability.AllowsScope(test.scope); got != test.want {
				t.Fatalf("AllowsScope(%q) = %v, want %v", test.scope, got, test.want)
			}
		})
	}
}
