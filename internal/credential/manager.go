package credential

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"time"

	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
)

type State interface {
	KeyStore
	CredentialDeliveryKey(context.Context, string) (computerstate.CredentialDeliveryKey, bool, error)
	CredentialBinding(context.Context, string) (computerstate.CredentialBinding, bool, error)
	CredentialBindingByDelivery(context.Context, string) (computerstate.CredentialBinding, bool, error)
	SaveCredentialBinding(context.Context, computerstate.CredentialBinding) error
}

type Delivery struct {
	ID                 string
	RequestID          string
	ComputerID         string
	AgentID            string
	CredentialKind     string
	KeyID              string
	EphemeralPublicKey [32]byte
	Nonce              [24]byte
	Ciphertext         []byte
	ExpiresAt          time.Time
}

type Manager struct {
	state    State
	facility Facility
	random   io.Reader
	now      func() time.Time
	key      computerstate.CredentialDeliveryKey
}

func NewManager(ctx context.Context, state State, facility Facility, random io.Reader, now func() time.Time) (*Manager, error) {
	if state == nil || facility == nil {
		return nil, errors.New("credential manager secure boundaries are required")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	key, err := EnsureDeliveryKey(ctx, state, random, now())
	if err != nil {
		return nil, err
	}
	return &Manager{state: state, facility: facility, random: random, now: now, key: key}, nil
}

func (manager *Manager) Key() computerstate.CredentialDeliveryKey { return manager.key }

func (manager *Manager) FacilityKind() string { return manager.facility.Kind() }

func (manager *Manager) Bind(ctx context.Context, delivery Delivery) (string, string, error) {
	if existing, found, err := manager.state.CredentialBindingByDelivery(ctx, delivery.ID); err != nil {
		return "", "binding_failed", err
	} else if found {
		if existing.AgentID != delivery.AgentID || existing.ComputerID != delivery.ComputerID || existing.CredentialKind != delivery.CredentialKind || existing.KeyID != delivery.KeyID {
			return "", "binding_failed", errors.New("credential delivery conflicts with existing binding")
		}
		return existing.Handle, "", nil
	}
	if !delivery.ExpiresAt.After(manager.now()) {
		return "", "decrypt_failed", errors.New("credential delivery expired")
	}
	key, found, err := manager.state.CredentialDeliveryKey(ctx, delivery.KeyID)
	if err != nil {
		return "", "key_unavailable", err
	}
	if !found {
		return "", "key_unavailable", errors.New("credential delivery key is unavailable")
	}
	plaintext, err := Open(key.PrivateKey, DeliveryContext{
		RequestID: delivery.RequestID, ComputerID: delivery.ComputerID, AgentID: delivery.AgentID,
		CredentialKind: delivery.CredentialKind, KeyID: delivery.KeyID, ExpiresAt: delivery.ExpiresAt,
	}, Sealed{EphemeralPublicKey: delivery.EphemeralPublicKey, Nonce: delivery.Nonce, Ciphertext: delivery.Ciphertext})
	if err != nil {
		return "", "decrypt_failed", err
	}
	defer clear(plaintext)
	handle, err := manager.newHandle()
	if err != nil {
		return "", "binding_failed", err
	}
	if err := manager.facility.Put(ctx, handle, plaintext); err != nil {
		return "", "store_unavailable", err
	}
	binding := computerstate.CredentialBinding{
		Handle: handle, DeliveryID: delivery.ID, AgentID: delivery.AgentID, ComputerID: delivery.ComputerID,
		CredentialKind: delivery.CredentialKind, KeyID: delivery.KeyID, CreatedAt: manager.now().UTC(),
	}
	if err := manager.state.SaveCredentialBinding(ctx, binding); err != nil {
		_ = manager.facility.Delete(context.WithoutCancel(ctx), handle)
		return "", "binding_failed", err
	}
	return handle, "", nil
}

func (manager *Manager) Resolve(ctx context.Context, handle, agentID, computerID, credentialKind string) ([]byte, error) {
	binding, found, err := manager.state.CredentialBinding(ctx, handle)
	if err != nil {
		return nil, err
	}
	if !found || binding.AgentID != agentID || binding.ComputerID != computerID || binding.CredentialKind != credentialKind {
		return nil, errors.New("credential binding does not match runtime placement")
	}
	return manager.facility.Get(ctx, handle)
}

func (manager *Manager) ValidateBinding(ctx context.Context, handle, agentID, computerID, credentialKind string) error {
	binding, found, err := manager.state.CredentialBinding(ctx, handle)
	if err != nil {
		return err
	}
	if !found || binding.AgentID != agentID || binding.ComputerID != computerID || binding.CredentialKind != credentialKind {
		return errors.New("credential binding does not match runtime placement")
	}
	return nil
}

func (manager *Manager) newHandle() (string, error) {
	payload := make([]byte, 24)
	if _, err := io.ReadFull(manager.random, payload); err != nil {
		return "", err
	}
	return "cred_" + base64.RawURLEncoding.EncodeToString(payload), nil
}
