package credential

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestSealMatchesBrowserFixture(t *testing.T) {
	privateKey, publicKey, delivery := browserFixture(t)
	sealed, err := Seal(newSealFixtureRandom(), publicKey, delivery, []byte("sk-browser-only"))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sealed.EphemeralPublicKey[:]); got != "5869aff450549732cbaaed5e5df9b30a6da31cb0e5742bad5ad4a1a768f1a67b" {
		t.Fatalf("ephemeral public key = %s", got)
	}
	if got := hex.EncodeToString(sealed.Nonce[:]); got != "4142434445464748494a4b4c4d4e4f505152535455565758" {
		t.Fatalf("nonce = %s", got)
	}
	if got := hex.EncodeToString(sealed.Ciphertext); got != "fe05d66ced0274b657cd10154777ecd58c1abdb08f1de0a5ce5c306ec8d2cf" {
		t.Fatalf("ciphertext = %s", got)
	}
	plaintext, err := Open(privateKey, delivery, sealed)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plaintext)
	if string(plaintext) != "sk-browser-only" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestOpenRejectsTamperingAndDifferentDeliveryContext(t *testing.T) {
	privateKey, publicKey, delivery := browserFixture(t)
	sealed, err := Seal(newSealFixtureRandom(), publicKey, delivery, []byte("sk-browser-only"))
	if err != nil {
		t.Fatal(err)
	}

	tampered := sealed
	tampered.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
	tampered.Ciphertext[0] ^= 0xff
	if plaintext, err := Open(privateKey, delivery, tampered); err == nil || plaintext != nil {
		t.Fatalf("tampered ciphertext returned plaintext %q, error %v", plaintext, err)
	}

	tests := map[string]func(DeliveryContext) DeliveryContext{
		"request": func(value DeliveryContext) DeliveryContext {
			value.RequestID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			return value
		},
		"computer": func(value DeliveryContext) DeliveryContext {
			value.ComputerID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			return value
		},
		"agent": func(value DeliveryContext) DeliveryContext {
			value.AgentID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			return value
		},
		"kind": func(value DeliveryContext) DeliveryContext { value.CredentialKind = "anthropic"; return value },
		"key": func(value DeliveryContext) DeliveryContext {
			value.KeyID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			return value
		},
		"expiry": func(value DeliveryContext) DeliveryContext {
			value.ExpiresAt = value.ExpiresAt.Add(time.Second)
			return value
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			if plaintext, err := Open(privateKey, change(delivery), sealed); err == nil || plaintext != nil {
				t.Fatalf("different %s returned plaintext %q, error %v", name, plaintext, err)
			}
		})
	}
}

func TestSealRejectsInvalidInput(t *testing.T) {
	_, publicKey, delivery := browserFixture(t)
	if _, err := Seal(bytes.NewReader(nil), publicKey, delivery, nil); err == nil {
		t.Fatal("Seal accepted empty plaintext")
	}
	if _, err := Seal(bytes.NewReader(nil), publicKey, delivery, make([]byte, 64*1024+1)); err == nil {
		t.Fatal("Seal accepted oversized plaintext")
	}
	if _, err := Seal(newSealFixtureRandom(), [32]byte{}, delivery, []byte("secret")); err == nil {
		t.Fatal("Seal accepted an invalid public key")
	}
	delivery.RequestID = ""
	if _, err := Seal(bytes.NewReader(nil), publicKey, delivery, []byte("secret")); err == nil {
		t.Fatal("Seal accepted incomplete delivery context")
	}
}

func browserFixture(t *testing.T) ([32]byte, [32]byte, DeliveryContext) {
	t.Helper()
	privateBytes := sequence(1, 32)
	private, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		t.Fatal(err)
	}
	var privateKey, publicKey [32]byte
	copy(privateKey[:], private.Bytes())
	copy(publicKey[:], private.PublicKey().Bytes())
	return privateKey, publicKey, DeliveryContext{
		RequestID:      "11111111-1111-4111-8111-111111111111",
		ComputerID:     "22222222-2222-4222-8222-222222222222",
		AgentID:        "33333333-3333-4333-8333-333333333333",
		CredentialKind: "openai",
		KeyID:          "44444444-4444-4444-8444-444444444444",
		ExpiresAt:      time.Date(2026, 7, 24, 12, 34, 56, 0, time.UTC),
	}
}

func sequence(first, last byte) []byte {
	result := make([]byte, int(last-first)+1)
	for index := range result {
		result[index] = first + byte(index)
	}
	return result
}

type sealFixtureRandom struct {
	chunks [][]byte
}

func newSealFixtureRandom() *sealFixtureRandom {
	return &sealFixtureRandom{chunks: [][]byte{sequence(33, 64), sequence(65, 88)}}
}

func (random *sealFixtureRandom) Read(destination []byte) (int, error) {
	if len(destination) == 1 {
		destination[0] = 0
		return 1, nil
	}
	if len(random.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := random.chunks[0]
	if len(destination) != len(chunk) {
		return 0, fmt.Errorf("fixture random read size = %d, want %d", len(destination), len(chunk))
	}
	copy(destination, chunk)
	random.chunks = random.chunks[1:]
	return len(destination), nil
}
