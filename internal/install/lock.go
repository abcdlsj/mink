package install

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type installLock struct {
	file *os.File
}

func acquireInstallLock(layout Layout) (*installLock, error) {
	descriptor, err := unix.Open(layout.LockFile, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, errors.New("open install lock")
	}
	file := os.NewFile(uintptr(descriptor), layout.LockFile)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		file.Close()
		return nil, errors.New("install lock is unsafe")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("another install operation is active")
	}
	return &installLock{file: file}, nil
}

func (lock *installLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	return lock.file.Close()
}
