package authority

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	migrationCredential      = "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"
	otherMigrationCredential = "BCDEFGHIJKLMNOPQRSTUVWXYZ-abcdefghijklmnopq"
)

func TestCredentialMigrationCrashRecoveryAndFinalize(t *testing.T) {
	root, currentDirectory := migrationDirectories(t)
	legacyPath := filepath.Join(root, "owner.key")
	currentPath := filepath.Join(currentDirectory, "human.key")
	writeMigrationCredential(t, legacyPath, migrationCredential)

	first, err := PrepareCredentialMigration(legacyPath, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.CredentialPath != currentPath {
		t.Fatalf("credential path = %q", first.CredentialPath)
	}
	if credential, err := ReadCredentialFile(currentPath); err != nil || credential != migrationCredential {
		t.Fatalf("published credential = %q, %v", credential, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy credential disappeared before finalize: %v", err)
	}

	recovered, err := PrepareCredentialMigration(legacyPath, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Finalize(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy credential remains after finalize: %v", err)
	}
	if credential, err := ReadCredentialFile(currentPath); err != nil || credential != migrationCredential {
		t.Fatalf("current credential = %q, %v", credential, err)
	}
}

func TestCredentialMigrationNoReplaceConcurrentPublish(t *testing.T) {
	for _, test := range []struct {
		name       string
		credential string
		wantError  bool
	}{
		{"same", migrationCredential, false},
		{"different", otherMigrationCredential, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, currentDirectory := migrationDirectories(t)
			legacyPath := filepath.Join(root, "owner.key")
			currentPath := filepath.Join(currentDirectory, "human.key")
			writeMigrationCredential(t, legacyPath, migrationCredential)
			previous := credentialMigrationBeforePublish
			credentialMigrationBeforePublish = func(path, _ string) {
				writeMigrationCredential(t, path, test.credential)
			}
			t.Cleanup(func() { credentialMigrationBeforePublish = previous })
			migration, err := PrepareCredentialMigration(legacyPath, currentPath)
			if migration != nil {
				migration.Close()
			}
			if (err != nil) != test.wantError {
				t.Fatalf("PrepareCredentialMigration() error = %v", err)
			}
			credential, readErr := ReadCredentialFile(currentPath)
			if readErr != nil || credential != test.credential {
				t.Fatalf("concurrent credential = %q, %v", credential, readErr)
			}
			if legacy, readErr := ReadCredentialFile(legacyPath); readErr != nil || legacy != migrationCredential {
				t.Fatalf("legacy credential = %q, %v", legacy, readErr)
			}
		})
	}
}

func TestCredentialMigrationRejectsMismatchAndUnsafeFiles(t *testing.T) {
	root, currentDirectory := migrationDirectories(t)
	legacyPath := filepath.Join(root, "owner.key")
	currentPath := filepath.Join(currentDirectory, "human.key")
	writeMigrationCredential(t, legacyPath, migrationCredential)
	writeMigrationCredential(t, currentPath, otherMigrationCredential)
	if _, err := PrepareCredentialMigration(legacyPath, currentPath); err == nil {
		t.Fatal("different credentials were accepted")
	}
	if err := os.Remove(currentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacyPath, currentPath); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareCredentialMigration(legacyPath, currentPath); err == nil {
		t.Fatal("symlink credential was accepted")
	}
}

func TestCredentialMigrationUnlinkFailureKeepsBothFiles(t *testing.T) {
	root, currentDirectory := migrationDirectories(t)
	legacyPath := filepath.Join(root, "owner.key")
	currentPath := filepath.Join(currentDirectory, "human.key")
	writeMigrationCredential(t, legacyPath, migrationCredential)
	migration, err := PrepareCredentialMigration(legacyPath, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	previous := credentialMigrationUnlink
	credentialMigrationUnlink = func(string) error { return errors.New("injected unlink failure") }
	t.Cleanup(func() { credentialMigrationUnlink = previous })
	if err := migration.Finalize(); err == nil {
		t.Fatal("injected unlink failure was ignored")
	}
	migration.Close()
	for _, path := range []string{legacyPath, currentPath} {
		if credential, err := ReadCredentialFile(path); err != nil || credential != migrationCredential {
			t.Fatalf("credential %q = %q, %v", path, credential, err)
		}
	}
}

func migrationDirectories(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "home")
	current := filepath.Join(t.TempDir(), "credentials")
	for _, path := range []string{root, current} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root, current
}

func writeMigrationCredential(t *testing.T, path, credential string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(credential), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
