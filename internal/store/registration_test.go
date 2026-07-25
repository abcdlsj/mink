package store

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/google/uuid"
)

func registerTestOwner(t *testing.T, db *Store, now time.Time) (AuthorityBootstrap, error) {
	t.Helper()
	return db.RegisterFirstOwner(context.Background(), authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity: authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password: testPasswordDigest(1), SessionToken: testBrowserToken(1),
		Now: now, SessionExpiresAt: now.Add(12 * time.Hour),
	})
}

func mustRegisterTestOwner(t *testing.T, db *Store, now time.Time) AuthorityBootstrap {
	t.Helper()
	bs, err := registerTestOwner(t, db, now)
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

func testPasswordDigest(fill byte) authorityapp.PasswordDigest {
	return authorityapp.PasswordDigest{
		Algorithm: "argon2id", Salt: bytes.Repeat([]byte{fill}, 16), Digest: bytes.Repeat([]byte{fill}, 32),
		Memory: 8192, Iterations: 1, Parallelism: 1,
	}
}

func testBrowserToken(fill byte) string {
	return fmt.Sprintf("%043d", fill)
}
