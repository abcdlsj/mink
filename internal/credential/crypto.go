package credential

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const Algorithm = "x25519_xchacha20_poly1305"

type KeyStore interface {
	ActiveCredentialDeliveryKey(context.Context) (computerstate.CredentialDeliveryKey, bool, error)
	SaveCredentialDeliveryKey(context.Context, computerstate.CredentialDeliveryKey) error
}

type DeliveryContext struct {
	RequestID      string
	ComputerID     string
	AgentID        string
	CredentialKind string
	KeyID          string
	ExpiresAt      time.Time
}

type Sealed struct {
	EphemeralPublicKey [32]byte
	Nonce              [chacha20poly1305.NonceSizeX]byte
	Ciphertext         []byte
}

func EnsureDeliveryKey(ctx context.Context, store KeyStore, random io.Reader, now time.Time) (computerstate.CredentialDeliveryKey, error) {
	if store == nil {
		return computerstate.CredentialDeliveryKey{}, errors.New("credential delivery key store is required")
	}
	if random == nil {
		random = rand.Reader
	}
	key, found, err := store.ActiveCredentialDeliveryKey(ctx)
	if err != nil || found {
		return key, err
	}
	privateKey, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return computerstate.CredentialDeliveryKey{}, fmt.Errorf("generate credential delivery key: %w", err)
	}
	key = computerstate.CredentialDeliveryKey{KeyID: uuid.NewString(), CreatedAt: now.UTC()}
	copy(key.PrivateKey[:], privateKey.Bytes())
	copy(key.PublicKey[:], privateKey.PublicKey().Bytes())
	if err := store.SaveCredentialDeliveryKey(ctx, key); err != nil {
		return computerstate.CredentialDeliveryKey{}, err
	}
	return key, nil
}

func Seal(random io.Reader, publicKey [32]byte, delivery DeliveryContext, plaintext []byte) (Sealed, error) {
	if random == nil {
		random = rand.Reader
	}
	if err := validateDeliveryContext(delivery); err != nil {
		return Sealed{}, err
	}
	if len(plaintext) == 0 || len(plaintext) > 64*1024 {
		return Sealed{}, errors.New("credential plaintext size is invalid")
	}
	peer, err := ecdh.X25519().NewPublicKey(publicKey[:])
	if err != nil {
		return Sealed{}, errors.New("credential delivery public key is invalid")
	}
	ephemeral, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return Sealed{}, fmt.Errorf("generate ephemeral credential key: %w", err)
	}
	shared, err := ephemeral.ECDH(peer)
	if err != nil {
		return Sealed{}, errors.New("derive credential delivery key")
	}
	key, associated, err := deriveKey(shared, delivery)
	clear(shared)
	if err != nil {
		return Sealed{}, err
	}
	aead, err := chacha20poly1305.NewX(key)
	clear(key)
	if err != nil {
		return Sealed{}, err
	}
	var sealed Sealed
	copy(sealed.EphemeralPublicKey[:], ephemeral.PublicKey().Bytes())
	if _, err := io.ReadFull(random, sealed.Nonce[:]); err != nil {
		return Sealed{}, fmt.Errorf("generate credential delivery nonce: %w", err)
	}
	sealed.Ciphertext = aead.Seal(nil, sealed.Nonce[:], plaintext, associated)
	return sealed, nil
}

func Open(privateKey [32]byte, delivery DeliveryContext, sealed Sealed) ([]byte, error) {
	if err := validateDeliveryContext(delivery); err != nil {
		return nil, err
	}
	private, err := ecdh.X25519().NewPrivateKey(privateKey[:])
	if err != nil {
		return nil, errors.New("credential delivery private key is invalid")
	}
	peer, err := ecdh.X25519().NewPublicKey(sealed.EphemeralPublicKey[:])
	if err != nil {
		return nil, errors.New("credential delivery ephemeral key is invalid")
	}
	shared, err := private.ECDH(peer)
	if err != nil {
		return nil, errors.New("derive credential delivery key")
	}
	key, associated, err := deriveKey(shared, delivery)
	clear(shared)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(key)
	clear(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, sealed.Nonce[:], sealed.Ciphertext, associated)
	if err != nil {
		return nil, errors.New("sealed credential authentication failed")
	}
	if len(plaintext) == 0 || len(plaintext) > 64*1024 {
		clear(plaintext)
		return nil, errors.New("credential plaintext size is invalid")
	}
	return plaintext, nil
}

func deriveKey(shared []byte, delivery DeliveryContext) ([]byte, []byte, error) {
	associated := associatedData(delivery)
	reader := hkdf.New(sha256.New, shared, nil, append([]byte("sumi.credential.delivery.v1\x00"), associated...))
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, nil, fmt.Errorf("derive credential encryption key: %w", err)
	}
	return key, associated, nil
}

func associatedData(delivery DeliveryContext) []byte {
	values := []string{delivery.RequestID, delivery.ComputerID, delivery.AgentID, delivery.CredentialKind, delivery.KeyID}
	size := 8
	for _, value := range values {
		size += 4 + len(value)
	}
	result := make([]byte, 0, size)
	for _, value := range values {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		result = append(result, length[:]...)
		result = append(result, value...)
	}
	var expiry [8]byte
	binary.BigEndian.PutUint64(expiry[:], uint64(delivery.ExpiresAt.UTC().Unix()))
	return append(result, expiry[:]...)
}

func validateDeliveryContext(delivery DeliveryContext) error {
	for _, value := range []string{delivery.RequestID, delivery.ComputerID, delivery.AgentID, delivery.CredentialKind, delivery.KeyID} {
		if value == "" {
			return errors.New("credential delivery context is incomplete")
		}
	}
	if delivery.ExpiresAt.IsZero() {
		return errors.New("credential delivery expiry is required")
	}
	return nil
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
