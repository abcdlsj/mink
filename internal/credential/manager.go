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

func (m *Manager) Key() computerstate.CredentialDeliveryKey { return m.key }

func (m *Manager) FacilityKind() string { return m.facility.Kind() }

func (m *Manager) Bind(ctx context.Context, d Delivery) (string, string, error) {
	if existing, found, err := m.state.CredentialBindingByDelivery(ctx, d.ID); err != nil {
		return "", "binding_failed", err
	} else if found {
		if existing.AgentID != d.AgentID || existing.ComputerID != d.ComputerID ||
			existing.CredentialKind != d.CredentialKind || existing.KeyID != d.KeyID {
			return "", "binding_failed", errors.New("credential delivery conflicts with existing binding")
		}
		return existing.Handle, "", nil
	}
	if !d.ExpiresAt.After(m.now()) {
		return "", "decrypt_failed", errors.New("credential delivery expired")
	}
	key, found, err := m.state.CredentialDeliveryKey(ctx, d.KeyID)
	if err != nil {
		return "", "key_unavailable", err
	}
	if !found {
		return "", "key_unavailable", errors.New("credential delivery key is unavailable")
	}
	plaintext, err := Open(key.PrivateKey, DeliveryContext{
		RequestID: d.RequestID, ComputerID: d.ComputerID, AgentID: d.AgentID,
		CredentialKind: d.CredentialKind, KeyID: d.KeyID, ExpiresAt: d.ExpiresAt,
	}, Sealed{EphemeralPublicKey: d.EphemeralPublicKey, Nonce: d.Nonce, Ciphertext: d.Ciphertext})
	if err != nil {
		return "", "decrypt_failed", err
	}
	defer clear(plaintext)
	handle, err := m.newHandle()
	if err != nil {
		return "", "binding_failed", err
	}
	if err := m.facility.Put(ctx, handle, plaintext); err != nil {
		return "", "store_unavailable", err
	}
	binding := computerstate.CredentialBinding{
		Handle: handle, DeliveryID: d.ID, AgentID: d.AgentID, ComputerID: d.ComputerID,
		CredentialKind: d.CredentialKind, KeyID: d.KeyID, CreatedAt: m.now().UTC(),
	}
	if err := m.state.SaveCredentialBinding(ctx, binding); err != nil {
		m.facility.Delete(context.WithoutCancel(ctx), handle)
		return "", "binding_failed", err
	}
	return handle, "", nil
}

func (m *Manager) Resolve(ctx context.Context, handle, agentID, computerID, credentialKind string) ([]byte, error) {
	binding, found, err := m.state.CredentialBinding(ctx, handle)
	if err != nil {
		return nil, err
	}
	if !found || binding.AgentID != agentID || binding.ComputerID != computerID || binding.CredentialKind != credentialKind {
		return nil, errors.New("credential binding does not match runtime placement")
	}
	return m.facility.Get(ctx, handle)
}

func (m *Manager) ValidateBinding(ctx context.Context, handle, agentID, computerID, credentialKind string) error {
	binding, found, err := m.state.CredentialBinding(ctx, handle)
	if err != nil {
		return err
	}
	if !found || binding.AgentID != agentID || binding.ComputerID != computerID || binding.CredentialKind != credentialKind {
		return errors.New("credential binding does not match runtime placement")
	}
	return nil
}

func (m *Manager) newHandle() (string, error) {
	payload := make([]byte, 24)
	if _, err := io.ReadFull(m.random, payload); err != nil {
		return "", err
	}
	return "cred_" + base64.RawURLEncoding.EncodeToString(payload), nil
}
