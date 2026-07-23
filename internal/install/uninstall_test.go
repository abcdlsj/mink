package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/osservice"
)

type observingUninstallServices struct {
	*fakeServices
	uninstallCalls int
}

func (services *observingUninstallServices) Uninstall(context.Context) error {
	services.uninstallCalls++
	return nil
}

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

func TestUninstallWaitsForStoppedServiceLeaseToDrain(t *testing.T) {
	manager, fake := testManager(t)
	services := &observingUninstallServices{fakeServices: fake}
	manager.Services = services
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	run, err := lifecycle.AcquireRun(manager.Layout.DataRoot, manager.Layout.RuntimeRoot, lifecycle.ComponentServer)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	now := time.Unix(0, 0)
	sleeps := 0
	setServiceMaintenanceClock(t, func() time.Time { return now }, func(context.Context, time.Duration) error {
		sleeps++
		return run.Close()
	})
	if err := manager.Uninstall(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if sleeps != 1 {
		t.Fatalf("maintenance drain sleeps = %d", sleeps)
	}
	if services.uninstallCalls != 1 {
		t.Fatalf("service uninstall calls = %d", services.uninstallCalls)
	}
	if _, err := os.Lstat(manager.Layout.InstallRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("install root remains after uninstall: %v", err)
	}
	if _, err := os.Stat(manager.Layout.DataRoot); err != nil {
		t.Fatalf("data root removed by preserve-data uninstall: %v", err)
	}
}

func TestUninstallStrayForegroundLeaseTimesOutWithoutSideEffects(t *testing.T) {
	manager, fake := testManager(t)
	services := &observingUninstallServices{fakeServices: fake}
	manager.Services = services
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	dataSentinel := filepath.Join(manager.Layout.DataRoot, "data", "uninstall-sentinel")
	if err := os.WriteFile(dataSentinel, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	installBefore := snapshotRegularFiles(t, manager.Layout.InstallRoot)
	run, err := lifecycle.AcquireRun(manager.Layout.DataRoot, manager.Layout.RuntimeRoot, lifecycle.ComponentServer)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	services.running[osservice.Server] = true
	services.running[osservice.Computer] = true
	now := time.Unix(0, 0)
	started := now
	sleeps := 0
	setServiceMaintenanceClock(t, func() time.Time { return now }, func(_ context.Context, duration time.Duration) error {
		sleeps++
		now = now.Add(duration)
		return nil
	})
	err = manager.Uninstall(context.Background(), false)
	if !errors.Is(err, lifecycle.ErrRuntimeActive) {
		t.Fatalf("uninstall beside foreground run = %v", err)
	}
	if sleeps == 0 {
		t.Fatal("uninstall did not wait for the stopped service lease to drain")
	}
	if elapsed := now.Sub(started); elapsed != serviceMaintenanceDrainTimeout {
		t.Fatalf("maintenance drain elapsed = %s", elapsed)
	}
	if services.uninstallCalls != 0 {
		t.Fatalf("service uninstall called beside foreground run: %d", services.uninstallCalls)
	}
	if services.running[osservice.Server] || services.running[osservice.Computer] {
		t.Fatalf("services restarted beside foreground run: %v", services.running)
	}
	if installAfter := snapshotRegularFiles(t, manager.Layout.InstallRoot); !reflect.DeepEqual(installAfter, installBefore) {
		t.Fatal("install tree changed beside foreground run")
	}
	if payload, err := os.ReadFile(dataSentinel); err != nil || string(payload) != "preserved" {
		t.Fatalf("data sentinel = %q, %v", payload, err)
	}
	if _, err := os.Lstat(manager.Layout.RestoreRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore point created beside foreground run: %v", err)
	}
	if _, err := os.Lstat(manager.Layout.VersionRoot("2.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate created beside foreground run: %v", err)
	}
}

func snapshotRegularFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			payload, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			result[relative] = payload
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
