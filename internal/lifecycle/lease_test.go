package lifecycle

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRunLeasesShareGateAndKeepComponentSingletons(t *testing.T) {
	dataRoot, runtimeRoot := leaseRoots(t)
	server, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	computer, err := AcquireRun(dataRoot, runtimeRoot, ComponentComputer)
	if err != nil {
		t.Fatal(err)
	}
	defer computer.Close()
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("second Server lease = %v", err)
	}
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentComputer); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("second Computer lease = %v", err)
	}
	if _, err := AcquireMaintenance(dataRoot, runtimeRoot); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("maintenance with run holders = %v", err)
	}
}

func TestMaintenanceBlocksRunsAndReleasesCleanly(t *testing.T) {
	dataRoot, runtimeRoot := leaseRoots(t)
	maintenance, err := AcquireMaintenance(dataRoot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("run during maintenance = %v", err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
	run, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer)
	if err != nil {
		t.Fatalf("run after maintenance release = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInheritedMaintenancePortableFlockSemantics(t *testing.T) {
	dataRoot, runtimeRoot := leaseRoots(t)
	gatePath, err := GatePath(dataRoot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := openLock(gatePath)
	if err != nil {
		t.Fatal(err)
	}
	seed.Close()

	independent, err := os.OpenFile(gatePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := AcquireInheritedMaintenance(dataRoot, runtimeRoot, independent)
	if err != nil {
		t.Fatalf("independent correct inode without holder = %v", err)
	}
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentComputer); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("run entered inherited maintenance = %v", err)
	}
	if err := inherited.Close(); err != nil {
		t.Fatal(err)
	}

	shared, err := AcquireRun(dataRoot, runtimeRoot, ComponentComputer)
	if err != nil {
		t.Fatal(err)
	}
	blockedFD, err := os.OpenFile(gatePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireInheritedMaintenance(dataRoot, runtimeRoot, blockedFD); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("independent FD with shared holder = %v", err)
	}
	blockedFD.Close()
	shared.Close()

	parent, err := AcquireMaintenance(dataRoot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	duplicateDescriptor, err := unix.Dup(int(parent.File().Fd()))
	if err != nil {
		t.Fatal(err)
	}
	duplicate := os.NewFile(uintptr(duplicateDescriptor), gatePath)
	child, err := AcquireInheritedMaintenance(dataRoot, runtimeRoot, duplicate)
	if err != nil {
		t.Fatalf("same OFD inherited lease = %v", err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("inherited child did not preserve exclusive lock = %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleRejectsUnsafeLockPath(t *testing.T) {
	dataRoot, runtimeRoot := leaseRoots(t)
	gatePath, err := GatePath(dataRoot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(runtimeRoot, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, gatePath); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer); err == nil {
		t.Fatal("symlink lock path was accepted")
	}
}

func TestPrepareMaintenanceChildUsesPrivateInheritedDescriptor(t *testing.T) {
	dataRoot, runtimeRoot := leaseRoots(t)
	maintenance, err := AcquireMaintenance(dataRoot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	command := exec.Command("ignored")
	command.Env = []string{"KEEP=value", maintenanceFDEnvironment + "=999"}
	if err := PrepareMaintenanceChild(maintenance, command); err != nil {
		t.Fatal(err)
	}
	if len(command.ExtraFiles) != 1 || command.ExtraFiles[0] != maintenance.File() {
		t.Fatalf("extra files = %+v", command.ExtraFiles)
	}
	joined := strings.Join(command.Env, "\n")
	if !strings.Contains(joined, "KEEP=value") || !strings.Contains(joined, maintenanceFDEnvironment+"=3") || strings.Contains(joined, "=999") {
		t.Fatalf("child environment = %q", joined)
	}
}

func TestAcquireRunConsumesInheritedMaintenanceWithoutDowngrade(t *testing.T) {
	dataRoot, runtimeRoot := leaseRoots(t)
	maintenance, err := AcquireMaintenance(dataRoot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	duplicateDescriptor, err := unix.Dup(int(maintenance.File().Fd()))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(maintenanceFDEnvironment, strconv.Itoa(duplicateDescriptor))
	run, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer)
	if err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(maintenanceFDEnvironment, "")
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentComputer); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("new run entered inherited probe = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRunRejectsUnheldInheritedGateWithSharedHolder(t *testing.T) {
	dataRoot, runtimeRoot := leaseRoots(t)
	shared, err := AcquireRun(dataRoot, runtimeRoot, ComponentComputer)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	gatePath, err := GatePath(dataRoot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := os.OpenFile(gatePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer independent.Close()
	duplicateDescriptor, err := unix.Dup(int(independent.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(maintenanceFDEnvironment, strconv.Itoa(duplicateDescriptor))
	if _, err := AcquireRun(dataRoot, runtimeRoot, ComponentServer); !errors.Is(err, ErrRuntimeActive) {
		t.Fatalf("probe entered beside shared holder = %v", err)
	}
}

func leaseRoots(t *testing.T) (string, string) {
	t.Helper()
	dataRoot := filepath.Join(t.TempDir(), "data")
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return dataRoot, runtimeRoot
}
