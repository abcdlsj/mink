package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallPreservesFiveEntriesAndReinstallRecovers(t *testing.T) {
	manager, _ := testManager(t)
	bundle := testBundle(t, "1.0.0")
	if err := manager.Install(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(manager.Layout.DataRoot, "data", "computer", "identity")
	if err := os.MkdirAll(filepath.Dir(identity), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte("identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if payload, err := os.ReadFile(identity); err != nil || string(payload) != "identity" {
		t.Fatalf("identity after uninstall = %q, %v", payload, err)
	}
	layout, err := Resolve(manager.Layout.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manager.Layout = layout
	if err := manager.Install(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Active(); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeRejectsSymlinkAndPreservesOutsideSentinel(t *testing.T) {
	manager, _ := testManager(t)
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(manager.Layout.DataRoot, "cache")
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), cache); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background(), true); err == nil {
		t.Fatal("purge accepted a symlink entry")
	}
	if payload, err := os.ReadFile(outside); err != nil || string(payload) != "sentinel" {
		t.Fatalf("outside sentinel = %q, %v", payload, err)
	}
}

func TestConfirmedPurgeRemovesOnlyDataRoot(t *testing.T) {
	manager, _ := testManager(t)
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	humanCredential := filepath.Join(manager.Layout.StateRoot, "credentials", "human.key")
	if err := os.WriteFile(humanCredential, []byte("private credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(manager.Layout.DataRoot); !os.IsNotExist(err) {
		t.Fatalf("data root remains: %v", err)
	}
	if _, err := os.Lstat(humanCredential); !os.IsNotExist(err) {
		t.Fatalf("human credential remains: %v", err)
	}
}
