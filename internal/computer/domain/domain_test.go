package domain

import (
	"encoding/base64"
	"testing"
)

func TestCapabilityInventoryValidity(t *testing.T) {
	valid := TrustedLocalCapabilityInventory(EngineCapability{
		Kind: EngineBuiltin, Version: "0.1.0", ProtocolVersion: 1,
		SupportsToolCalls: true, SupportsCancel: true, OpenAIResponses: true, Healthy: true,
	})
	tests := []struct {
		name      string
		inventory CapabilityInventory
		want      bool
	}{
		{"valid", valid, true},
		{"missing sandbox", CapabilityInventory{Engines: valid.Engines}, false},
		{"duplicate engine", CapabilityInventory{Engines: append(valid.Engines, valid.Engines[0]), Sandboxes: valid.Sandboxes}, false},
		{"invalid engine", TrustedLocalCapabilityInventory(EngineCapability{Kind: EngineBuiltin}), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.inventory.ValidDeclaration(); got != test.want {
				t.Fatalf("ValidDeclaration() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCredentialDeliveryCapabilityRequiresCanonicalX25519Key(t *testing.T) {
	publicKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	valid := CredentialDeliveryCapability{
		Healthy: true, Algorithm: "x25519_xchacha20_poly1305", Store: "macos_keychain", KeyID: "key-1", PublicKey: publicKey,
	}
	if !valid.Valid() {
		t.Fatal("valid credential delivery capability was rejected")
	}
	valid.PublicKey = "not-a-key"
	if valid.Valid() {
		t.Fatal("invalid credential delivery key was accepted")
	}
}
