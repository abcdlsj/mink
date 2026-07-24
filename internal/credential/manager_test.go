package credential

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
)

func TestManagerBindsOnceAndResolvesOnlyExactPlacement(t *testing.T) {
	privateKey, publicKey, deliveryContext := browserFixture(t)
	now := deliveryContext.ExpiresAt.Add(-time.Minute)
	state := &memoryState{key: computerstate.CredentialDeliveryKey{
		KeyID: deliveryContext.KeyID, PrivateKey: privateKey, PublicKey: publicKey, CreatedAt: now,
	}, bindings: make(map[string]computerstate.CredentialBinding), deliveries: make(map[string]string)}
	facility := &memoryFacility{secrets: make(map[string][]byte)}
	manager, err := NewManager(context.Background(), state, facility, bytes.NewReader(sequence(89, 112)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(newSealFixtureRandom(), publicKey, deliveryContext, []byte("sk-browser-only"))
	if err != nil {
		t.Fatal(err)
	}
	delivery := Delivery{
		ID: "55555555-5555-4555-8555-555555555555", RequestID: deliveryContext.RequestID,
		ComputerID: deliveryContext.ComputerID, AgentID: deliveryContext.AgentID,
		CredentialKind: deliveryContext.CredentialKind, KeyID: deliveryContext.KeyID,
		EphemeralPublicKey: sealed.EphemeralPublicKey, Nonce: sealed.Nonce,
		Ciphertext: sealed.Ciphertext, ExpiresAt: deliveryContext.ExpiresAt,
	}
	handle, errorCode, err := manager.Bind(context.Background(), delivery)
	if err != nil || errorCode != "" {
		t.Fatalf("Bind() = (%q, %q, %v)", handle, errorCode, err)
	}
	if handle != "cred_WVpbXF1eX2BhYmNkZWZnaGlqa2xtbm9w" {
		t.Fatalf("handle = %q", handle)
	}
	if got := string(facility.secrets[handle]); got != "sk-browser-only" {
		t.Fatalf("secure facility secret = %q", got)
	}
	binding := state.bindings[handle]
	if binding.Handle != handle || binding.DeliveryID != delivery.ID || binding.AgentID != delivery.AgentID ||
		binding.ComputerID != delivery.ComputerID || binding.CredentialKind != delivery.CredentialKind || binding.KeyID != delivery.KeyID {
		t.Fatalf("persisted binding = %+v", binding)
	}

	replayed, errorCode, err := manager.Bind(context.Background(), delivery)
	if err != nil || errorCode != "" || replayed != handle || facility.puts != 1 {
		t.Fatalf("replayed Bind() = (%q, %q, %v), puts = %d", replayed, errorCode, err, facility.puts)
	}
	resolved, err := manager.Resolve(context.Background(), handle, delivery.AgentID, delivery.ComputerID, delivery.CredentialKind)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(resolved)
	if string(resolved) != "sk-browser-only" {
		t.Fatalf("resolved secret = %q", resolved)
	}
	for name, values := range map[string][3]string{
		"agent":    {"another-agent", delivery.ComputerID, delivery.CredentialKind},
		"computer": {delivery.AgentID, "another-computer", delivery.CredentialKind},
		"kind":     {delivery.AgentID, delivery.ComputerID, "anthropic"},
	} {
		t.Run(name, func(t *testing.T) {
			if secret, err := manager.Resolve(context.Background(), handle, values[0], values[1], values[2]); err == nil || secret != nil {
				t.Fatalf("Resolve returned secret %q, error %v", secret, err)
			}
		})
	}
}

func TestManagerRejectsDeliveryReplayWithDifferentBinding(t *testing.T) {
	privateKey, publicKey, delivery := browserFixture(t)
	now := delivery.ExpiresAt.Add(-time.Minute)
	state := &memoryState{
		key: computerstate.CredentialDeliveryKey{KeyID: delivery.KeyID, PrivateKey: privateKey, PublicKey: publicKey},
		bindings: map[string]computerstate.CredentialBinding{"cred_existing_binding": {
			Handle: "cred_existing_binding", DeliveryID: "delivery", AgentID: delivery.AgentID,
			ComputerID: delivery.ComputerID, CredentialKind: "anthropic", KeyID: delivery.KeyID,
		}},
		deliveries: map[string]string{"delivery": "cred_existing_binding"},
	}
	manager, err := NewManager(context.Background(), state, &memoryFacility{secrets: make(map[string][]byte)}, bytes.NewReader(sequence(1, 24)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	_, errorCode, err := manager.Bind(context.Background(), Delivery{
		ID: "delivery", AgentID: delivery.AgentID, ComputerID: delivery.ComputerID,
		CredentialKind: delivery.CredentialKind, KeyID: delivery.KeyID, ExpiresAt: delivery.ExpiresAt,
	})
	if err == nil || errorCode != "binding_failed" {
		t.Fatalf("Bind() error code = %q, error = %v", errorCode, err)
	}
}

type memoryState struct {
	key        computerstate.CredentialDeliveryKey
	bindings   map[string]computerstate.CredentialBinding
	deliveries map[string]string
}

func (state *memoryState) ActiveCredentialDeliveryKey(context.Context) (computerstate.CredentialDeliveryKey, bool, error) {
	return state.key, state.key.KeyID != "", nil
}

func (state *memoryState) SaveCredentialDeliveryKey(_ context.Context, key computerstate.CredentialDeliveryKey) error {
	state.key = key
	return nil
}

func (state *memoryState) CredentialDeliveryKey(_ context.Context, keyID string) (computerstate.CredentialDeliveryKey, bool, error) {
	return state.key, state.key.KeyID == keyID, nil
}

func (state *memoryState) CredentialBinding(_ context.Context, handle string) (computerstate.CredentialBinding, bool, error) {
	binding, found := state.bindings[handle]
	return binding, found, nil
}

func (state *memoryState) CredentialBindingByDelivery(_ context.Context, deliveryID string) (computerstate.CredentialBinding, bool, error) {
	handle, found := state.deliveries[deliveryID]
	if !found {
		return computerstate.CredentialBinding{}, false, nil
	}
	binding, found := state.bindings[handle]
	return binding, found, nil
}

func (state *memoryState) SaveCredentialBinding(_ context.Context, binding computerstate.CredentialBinding) error {
	if _, found := state.bindings[binding.Handle]; found {
		return errors.New("binding exists")
	}
	state.bindings[binding.Handle] = binding
	state.deliveries[binding.DeliveryID] = binding.Handle
	return nil
}

type memoryFacility struct {
	secrets map[string][]byte
	puts    int
}

func (facility *memoryFacility) Kind() string { return "memory_test" }

func (facility *memoryFacility) Put(_ context.Context, handle string, secret []byte) error {
	facility.puts++
	facility.secrets[handle] = append([]byte(nil), secret...)
	return nil
}

func (facility *memoryFacility) Get(_ context.Context, handle string) ([]byte, error) {
	secret, found := facility.secrets[handle]
	if !found {
		return nil, errors.New("secret not found")
	}
	return append([]byte(nil), secret...), nil
}

func (facility *memoryFacility) Delete(_ context.Context, handle string) error {
	delete(facility.secrets, handle)
	return nil
}
