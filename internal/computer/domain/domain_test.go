package domain

import "testing"

func TestSandboxCapabilityValidity(t *testing.T) {
	tests := []struct {
		name       string
		capability SandboxCapability
		want       bool
	}{
		{"unknown", UnknownSandboxCapability(), true},
		{"trusted local", TrustedLocalSandboxCapability(), true},
		{"zero", SandboxCapability{}, false},
		{"partial", SandboxCapability{Provider: "trusted_local"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.capability.Valid(); got != test.want {
				t.Fatalf("Valid() = %v, want %v", got, test.want)
			}
		})
	}
}
