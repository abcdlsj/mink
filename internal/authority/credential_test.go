package authority

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureBootstrapCredentialCreatesAndReusesSecureFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.key")
	first, err := EnsureBootstrapCredential(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidCredential(first) {
		t.Fatalf("generated credential = %q", first)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("credential mode = %v", info.Mode())
	}
	second, err := EnsureBootstrapCredential(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("credential changed across reuse")
	}
}

func TestEnsureBootstrapCredentialRejectsMissingExistingAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.key")
	if _, err := EnsureBootstrapCredential(path, true); err == nil {
		t.Fatal("missing credential was recreated for existing authority")
	}
}

func TestReadCredentialFileRejectsSymlinkAndLooseMode(t *testing.T) {
	root := t.TempDir()
	credential := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOPQ"
	loose := filepath.Join(root, "loose.key")
	if err := os.WriteFile(loose, []byte(credential), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentialFile(loose); err == nil {
		t.Fatal("loose credential mode was accepted")
	}
	secure := filepath.Join(root, "secure.key")
	if err := os.WriteFile(secure, []byte(credential), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.key")
	if err := os.Symlink(secure, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentialFile(link); err == nil {
		t.Fatal("credential symlink was accepted")
	}
}
