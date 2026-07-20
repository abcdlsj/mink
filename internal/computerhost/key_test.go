package computerhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRegistrationKeyRequiresPrivateRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computer.key")
	if err := os.WriteFile(path, []byte("persistent-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := ReadRegistrationKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if key != "persistent-key" {
		t.Fatalf("key = %q", key)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegistrationKey(path); err == nil || strings.Contains(err.Error(), "persistent-key") {
		t.Fatalf("permission error = %v", err)
	}
}

func TestReadRegistrationKeyRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "computer.key")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegistrationKey(path); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("symlink error = %v", err)
	}
}
