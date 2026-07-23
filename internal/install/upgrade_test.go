package install

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/osservice"
)

func TestSQLiteIntegrityRequiresFinalSchemaMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE system_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO system_metadata(key, value) VALUES ('schema_version', '3');
	`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sqliteIntegrity(path); err != nil {
		t.Fatalf("final schema marker rejected: %v", err)
	}
}

func TestSQLiteIntegrityRejectsPreviousFinalSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE system_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO system_metadata(key, value) VALUES ('schema_version', '1');
	`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sqliteIntegrity(path); err == nil {
		t.Fatal("previous final schema passed current candidate probe")
	}
}

func TestSQLiteIntegrityRejectsLegacyGooseSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY, version_id INTEGER NOT NULL, is_applied BOOLEAN NOT NULL, tstamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO goose_db_version(version_id, is_applied) VALUES (19, TRUE);
		CREATE TABLE system_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sqliteIntegrity(path); err == nil {
		t.Fatal("legacy Goose schema passed final schema probe")
	}
}

func TestUpgradeSwitchesVersionAndNeverTouchesCASWorkspaceOrComputerState(t *testing.T) {
	manager, services := testManager(t)
	prober := &fakeProber{}
	manager.Prober = prober
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	sentinels := seedUpgradeFacts(t, manager.Layout)
	if err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0")); err != nil {
		t.Fatal(err)
	}
	active, err := manager.Active()
	if err != nil || active.Release.ReleaseVersion != "2.0.0" {
		t.Fatalf("active = %+v, %v", active, err)
	}
	if len(prober.probed) != 1 || prober.probed[0] != "2.0.0" {
		t.Fatalf("probes = %v", prober.probed)
	}
	if !services.running["server"] || !services.running["computer"] {
		t.Fatalf("services = %v", services.running)
	}
	assertSentinels(t, sentinels)
}

func TestFailedCandidateRestoresExactServerConfigAndManifestThenStartsOld(t *testing.T) {
	manager, services := testManager(t)
	prober := &fakeProber{failVersion: "2.0.0"}
	manager.Prober = prober
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	sentinels := seedUpgradeFacts(t, manager.Layout)
	before := snapshotBytes(t, manager.Layout)
	err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0"))
	if err == nil || errors.Is(err, ErrRestoreUnproven) {
		t.Fatalf("upgrade error = %v", err)
	}
	after := snapshotBytes(t, manager.Layout)
	for path, payload := range before {
		if string(after[path]) != string(payload) {
			t.Fatalf("restored %s differs: %q / %q", path, payload, after[path])
		}
	}
	if len(prober.probed) != 2 || prober.probed[0] != "2.0.0" || prober.probed[1] != "1.0.0" {
		t.Fatalf("probe order = %v", prober.probed)
	}
	if !services.running["server"] || !services.running["computer"] {
		t.Fatalf("old services were not restarted: %v", services.running)
	}
	assertSentinels(t, sentinels)
}

func TestTamperedRestoreReceiptKeepsServicesStopped(t *testing.T) {
	manager, services := testManager(t)
	manager.Prober = &fakeProber{failVersion: "2.0.0"}
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	seedUpgradeFacts(t, manager.Layout)
	manager.Before = func(stage string) error {
		if stage == "before-restore" {
			return os.WriteFile(filepath.Join(manager.Layout.RestoreRoot, "server.db"), []byte("tampered"), 0o600)
		}
		return nil
	}
	err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0"))
	if !errors.Is(err, ErrRestoreUnproven) {
		t.Fatalf("upgrade error = %v", err)
	}
	if services.running["server"] || services.running["computer"] {
		t.Fatalf("services restarted after unproven restore: %v", services.running)
	}
	if _, err := os.Stat(manager.Layout.RestoreRoot); err != nil {
		t.Fatalf("restore evidence was removed: %v", err)
	}
}

func TestUpgradeMutationPointsRestoreOldFactsAndServices(t *testing.T) {
	for _, stage := range []string{"after-snapshot", "after-copy", "before-candidate-probe", "after-candidate-probe", "before-switch", "after-switch"} {
		t.Run(stage, func(t *testing.T) {
			manager, services := testManager(t)
			manager.Prober = &fakeProber{mutateVersion: "2.0.0"}
			if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
				t.Fatal(err)
			}
			sentinels := seedUpgradeFacts(t, manager.Layout)
			before := snapshotBytes(t, manager.Layout)
			manager.Before = func(actual string) error {
				if actual == stage {
					return errors.New("injected mutation failure")
				}
				return nil
			}
			if err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0")); err == nil || errors.Is(err, ErrRestoreUnproven) {
				t.Fatalf("upgrade error = %v", err)
			}
			after := snapshotBytes(t, manager.Layout)
			for path, payload := range before {
				if string(after[path]) != string(payload) {
					t.Fatalf("%s was not restored", path)
				}
			}
			if !services.running[osservice.Server] || !services.running[osservice.Computer] {
				t.Fatalf("old services were not restarted: %v", services.running)
			}
			assertSentinels(t, sentinels)
		})
	}
}

func TestUnprovenRestoreStagesKeepServicesStopped(t *testing.T) {
	for _, stage := range []string{"before-restore", "after-restore", "before-old-probe", "before-old-start"} {
		t.Run(stage, func(t *testing.T) {
			manager, services := testManager(t)
			manager.Prober = &fakeProber{failVersion: "2.0.0"}
			if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
				t.Fatal(err)
			}
			seedUpgradeFacts(t, manager.Layout)
			manager.Before = func(actual string) error {
				if actual == stage {
					return errors.New("injected recovery failure")
				}
				return nil
			}
			err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0"))
			if !errors.Is(err, ErrRestoreUnproven) {
				t.Fatalf("upgrade error = %v", err)
			}
			if services.running[osservice.Server] || services.running[osservice.Computer] {
				t.Fatalf("services restarted after %s: %v", stage, services.running)
			}
		})
	}
}

func TestOldComputerStartFailureKeepsBothServicesStopped(t *testing.T) {
	manager, services := testManager(t)
	manager.Prober = &fakeProber{failVersion: "2.0.0"}
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	seedUpgradeFacts(t, manager.Layout)
	services.failStart = osservice.Computer
	err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0"))
	if !errors.Is(err, ErrRestoreUnproven) {
		t.Fatalf("upgrade error = %v", err)
	}
	if services.running[osservice.Server] || services.running[osservice.Computer] {
		t.Fatalf("services remain active after old start failure: %v", services.running)
	}
}

func TestUpgradeWaitsForStoppedServiceLeaseToDrain(t *testing.T) {
	manager, services := testManager(t)
	prober := &fakeProber{}
	manager.Prober = prober
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	seedUpgradeFacts(t, manager.Layout)
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
	if err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0")); err != nil {
		t.Fatal(err)
	}
	if sleeps != 1 {
		t.Fatalf("maintenance drain sleeps = %d", sleeps)
	}
	active, err := manager.Active()
	if err != nil || active.Release.ReleaseVersion != "2.0.0" {
		t.Fatalf("active = %+v, %v", active, err)
	}
	if len(prober.probed) != 1 || prober.probed[0] != "2.0.0" {
		t.Fatalf("probes = %v", prober.probed)
	}
	if !services.running[osservice.Server] || !services.running[osservice.Computer] {
		t.Fatalf("services = %v", services.running)
	}
}

func TestStrayForegroundLeaseTimesOutBeforeUpgradeSideEffects(t *testing.T) {
	manager, services := testManager(t)
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	seedUpgradeFacts(t, manager.Layout)
	activeBefore, err := os.ReadFile(manager.Layout.ActiveManifest)
	if err != nil {
		t.Fatal(err)
	}
	run, err := lifecycle.AcquireRun(manager.Layout.DataRoot, manager.Layout.RuntimeRoot, lifecycle.ComponentServer)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	now := time.Unix(0, 0)
	started := now
	sleeps := 0
	setServiceMaintenanceClock(t, func() time.Time { return now }, func(_ context.Context, duration time.Duration) error {
		sleeps++
		now = now.Add(duration)
		return nil
	})
	if err := manager.Upgrade(context.Background(), testBundle(t, "2.0.0")); !errors.Is(err, lifecycle.ErrRuntimeActive) {
		t.Fatalf("upgrade beside foreground run = %v", err)
	}
	if sleeps == 0 {
		t.Fatal("upgrade did not wait for the stopped service lease to drain")
	}
	if elapsed := now.Sub(started); elapsed != serviceMaintenanceDrainTimeout {
		t.Fatalf("maintenance drain elapsed = %s", elapsed)
	}
	if _, err := os.Lstat(manager.Layout.RestoreRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot created beside foreground run: %v", err)
	}
	if _, err := os.Lstat(manager.Layout.VersionRoot("2.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate copied beside foreground run: %v", err)
	}
	activeAfter, err := os.ReadFile(manager.Layout.ActiveManifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(activeAfter) != string(activeBefore) {
		t.Fatal("active manifest changed beside foreground run")
	}
	if services.running[osservice.Server] || services.running[osservice.Computer] {
		t.Fatalf("services restarted beside foreground run: %v", services.running)
	}
}

func setServiceMaintenanceClock(t *testing.T, now func() time.Time, sleep func(context.Context, time.Duration) error) {
	t.Helper()
	previousNow := serviceMaintenanceNow
	previousSleep := serviceMaintenanceSleep
	serviceMaintenanceNow = now
	serviceMaintenanceSleep = sleep
	t.Cleanup(func() {
		serviceMaintenanceNow = previousNow
		serviceMaintenanceSleep = previousSleep
	})
}

func seedUpgradeFacts(t *testing.T, layout Layout) map[string][]byte {
	t.Helper()
	serverDatabase := filepath.Join(layout.DataRoot, "data", "server.db")
	if err := os.WriteFile(serverDatabase, []byte("server-before"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := map[string][]byte{
		filepath.Join(layout.DataRoot, "data", "artifacts", "cas-extra"): []byte("cas"),
		filepath.Join(layout.DataRoot, "agents", "workspace", "fact"):    []byte("workspace"),
		filepath.Join(layout.DataRoot, "data", "computer", "state.db"):   []byte("computer-state"),
	}
	for path, payload := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func assertSentinels(t *testing.T, sentinels map[string][]byte) {
	t.Helper()
	for path, want := range sentinels {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("sentinel %s = %q, %v", path, got, err)
		}
	}
}

func snapshotBytes(t *testing.T, layout Layout) map[string][]byte {
	t.Helper()
	paths := []string{filepath.Join(layout.DataRoot, "data", "server.db"), filepath.Join(layout.DataRoot, "config.toml"), layout.ActiveManifest}
	result := map[string][]byte{}
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		result[path] = payload
	}
	return result
}
