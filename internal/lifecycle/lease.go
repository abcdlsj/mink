package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

var ErrRuntimeActive = errors.New("sumi runtime is already active")

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

func GatePath(dataRoot, runtimeRoot string) (string, error) {
	gate, _, err := lockPaths(dataRoot, runtimeRoot, ComponentServer)
	return gate, err
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
