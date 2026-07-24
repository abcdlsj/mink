package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func pairTestComputer(
	t *testing.T,
	database *Store,
	actor Principal,
	registrationKey string,
	inventory CapabilityInventory,
	now time.Time,
) Computer {
	t.Helper()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if _, err := database.CreateComputerPairing(context.Background(), CreateComputerPairingParams{
		RequestID: uuid.NewString(),
		Actor:     actor,
		Token:     token,
		ExpiresAt: now.Add(time.Minute),
		Now:       now,
	}); err != nil {
		t.Fatal(err)
	}
	computer, err := database.PairComputer(context.Background(), PairComputerParams{
		RequestID:           uuid.NewString(),
		PairingToken:        token,
		RegistrationKey:     registrationKey,
		Name:                "test-computer",
		OS:                  "linux",
		Arch:                "amd64",
		CapabilityInventory: inventory,
		Now:                 now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return computer
}

func bindTestRuntimeCredential(t *testing.T, database *Store, agentID, computerID, handle, kind string, now time.Time) {
	t.Helper()
	var humanID string
	if err := database.db.QueryRow(`SELECT id FROM humans ORDER BY created_at LIMIT 1`).Scan(&humanID); err != nil {
		t.Fatal(err)
	}
	deliveryID := uuid.NewString()
	requestID := uuid.NewString()
	if _, err := database.db.Exec(`
		INSERT INTO credential_deliveries(
			id, request_id, actor_human_id, payload_fingerprint, computer_id, agent_id,
			credential_kind, algorithm, key_id, ephemeral_public_key, nonce, ciphertext,
			state, binding_handle, error_code, expires_at, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, 'x25519_xchacha20_poly1305', 'test-key', ?, ?, ?, 'succeeded', ?, '', ?, ?, ?)
	`, deliveryID, requestID, humanID, make([]byte, 32), computerID, agentID, kind,
		make([]byte, 32), make([]byte, 24), make([]byte, 17), handle,
		unixNano(now.Add(time.Hour)), unixNano(now), unixNano(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`
		INSERT INTO credential_bindings(handle, delivery_id, computer_id, agent_id, credential_kind, key_id, created_at)
		VALUES(?, ?, ?, ?, ?, 'test-key', ?)
	`, handle, deliveryID, computerID, agentID, kind, unixNano(now)); err != nil {
		t.Fatal(err)
	}
}

func testCredentialHandle(agentID, computerID string) string {
	return "cred_test_" + strings.ReplaceAll(agentID, "-", "") + "_" + strings.ReplaceAll(computerID, "-", "")
}
