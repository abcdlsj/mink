package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

var ErrRuntimeActive = errors.New("sumi runtime is already active")

const maintenanceFDEnvironment = "SUMI_INTERNAL_MAINTENANCE_FD"

type Component string

const (
	ComponentServer   Component = "server"
	ComponentComputer Component = "computer"
)

type Lease struct {
	mu        sync.Mutex
	gate      *os.File
	component *os.File
	closed    bool
}

func AcquireRun(dataRoot, runtimeRoot string, component Component) (*Lease, error) {
	if component != ComponentServer && component != ComponentComputer {
		return nil, errors.New("runtime component is invalid")
	}
	gatePath, componentPath, err := lockPaths(dataRoot, runtimeRoot, component)
	if err != nil {
		return nil, err
	}
	if rawDescriptor := os.Getenv(maintenanceFDEnvironment); rawDescriptor != "" {
		return acquireInheritedRun(dataRoot, runtimeRoot, componentPath, rawDescriptor)
	}
	gate, err := openLock(gatePath)
	if err != nil {
		return nil, err
	}
	if err := flock(gate, unix.LOCK_SH|unix.LOCK_NB); err != nil {
		gate.Close()
		return nil, ErrRuntimeActive
	}
	componentFile, err := openLock(componentPath)
	if err != nil {
		unlockClose(gate)
		return nil, err
	}
	if err := flock(componentFile, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		componentFile.Close()
		unlockClose(gate)
		return nil, ErrRuntimeActive
	}
	return &Lease{gate: gate, component: componentFile}, nil
}

// PrepareMaintenanceChild transfers the current maintenance gate to a probe
// process without exposing an operator-facing flag or persistent setting.
func PrepareMaintenanceChild(lease *Lease, command *exec.Cmd) error {
	if lease == nil || command == nil {
		return errors.New("maintenance child is invalid")
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.gate == nil || lease.component != nil {
		return errors.New("maintenance lease is unavailable")
	}
	descriptor := 3 + len(command.ExtraFiles)
	command.ExtraFiles = append(command.ExtraFiles, lease.gate)
	environment := command.Env
	if environment == nil {
		environment = os.Environ()
	}
	prefix := maintenanceFDEnvironment + "="
	filtered := environment[:0]
	for _, value := range environment {
		if !strings.HasPrefix(value, prefix) {
			filtered = append(filtered, value)
		}
	}
	command.Env = append(filtered, prefix+strconv.Itoa(descriptor))
	return nil
}

func acquireInheritedRun(dataRoot, runtimeRoot, componentPath, rawDescriptor string) (*Lease, error) {
	descriptor, err := strconv.Atoi(rawDescriptor)
	if err != nil || descriptor < 3 {
		return nil, errors.New("inherited maintenance descriptor is invalid")
	}
	inherited := os.NewFile(uintptr(descriptor), "sumi-maintenance-gate")
	if inherited == nil {
		return nil, errors.New("inherited maintenance descriptor is invalid")
	}
	maintenance, err := AcquireInheritedMaintenance(dataRoot, runtimeRoot, inherited)
	if err != nil {
		inherited.Close()
		return nil, err
	}
	componentFile, err := openLock(componentPath)
	if err != nil {
		maintenance.Close()
		return nil, err
	}
	if err := flock(componentFile, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		componentFile.Close()
		maintenance.Close()
		return nil, ErrRuntimeActive
	}
	maintenance.component = componentFile
	return maintenance, nil
}

func AcquireMaintenance(dataRoot, runtimeRoot string) (*Lease, error) {
	gatePath, _, err := lockPaths(dataRoot, runtimeRoot, ComponentServer)
	if err != nil {
		return nil, err
	}
	gate, err := openLock(gatePath)
	if err != nil {
		return nil, err
	}
	if err := flock(gate, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		gate.Close()
		return nil, ErrRuntimeActive
	}
	return &Lease{gate: gate}, nil
}

func AcquireInheritedMaintenance(dataRoot, runtimeRoot string, inherited *os.File) (*Lease, error) {
	if inherited == nil {
		return nil, errors.New("inherited maintenance descriptor is invalid")
	}
	gatePath, _, err := lockPaths(dataRoot, runtimeRoot, ComponentServer)
	if err != nil {
		return nil, err
	}
	expected, err := openLock(gatePath)
	if err != nil {
		return nil, err
	}
	expectedInfo, expectedErr := expected.Stat()
	inheritedInfo, inheritedErr := inherited.Stat()
	expected.Close()
	if expectedErr != nil || inheritedErr != nil || !os.SameFile(expectedInfo, inheritedInfo) {
		return nil, errors.New("inherited maintenance descriptor does not match the runtime gate")
	}
	if err := flock(inherited, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, ErrRuntimeActive
	}
	return &Lease{gate: inherited}, nil
}

func (lease *Lease) File() *os.File {
	if lease == nil {
		return nil
	}
	return lease.gate
}

func (lease *Lease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	lease.closed = true
	var failures []error
	if lease.component != nil {
		if err := unlockClose(lease.component); err != nil {
			failures = append(failures, err)
		}
	}
	if lease.gate != nil {
		if err := unlockClose(lease.gate); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func lockPaths(dataRoot, runtimeRoot string, component Component) (string, string, error) {
	canonical, err := canonicalDataRoot(dataRoot)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(runtimeRoot)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", "", errors.New("sumi runtime directory is unsafe")
	}
	digest := sha256.Sum256([]byte(canonical))
	prefix := "root-" + hex.EncodeToString(digest[:16])
	return filepath.Join(runtimeRoot, prefix+".gate.lock"), filepath.Join(runtimeRoot, prefix+"."+string(component)+".lock"), nil
}

func canonicalDataRoot(path string) (string, error) {
	if path == "" {
		return "", errors.New("data root is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("resolve data root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("data root is unsafe")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", errors.New("resolve canonical data root")
	}
	return filepath.Clean(canonical), nil
}

func openLock(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		descriptor, err = unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, errors.New("open Sumi runtime lock")
	}
	file := os.NewFile(uintptr(descriptor), path)
	if created {
		if err := file.Chmod(0o600); err != nil {
			file.Close()
			return nil, errors.New("secure Sumi runtime lock")
		}
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		file.Close()
		return nil, errors.New("sumi runtime lock is unsafe")
	}
	return file, nil
}

func flock(file *os.File, operation int) error {
	if file == nil {
		return errors.New("runtime lock is unavailable")
	}
	if err := unix.Flock(int(file.Fd()), operation); err != nil {
		return fmt.Errorf("lock runtime gate: %w", err)
	}
	return nil
}

func unlockClose(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}
