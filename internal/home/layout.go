package home

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/abcdlsj/sumi/internal/configfile"
	"golang.org/x/sys/unix"
)

type Layout struct {
	Root      string
	Config    string
	Data      string
	Artifacts string
	Agents    string
	Cache     string
	Logs      string
	Database  string
}

func Ensure(root string) (Layout, error) {
	if root == "" {
		return Layout{}, fmt.Errorf("data root is required")
	}

	layout := Layout{
		Root:      root,
		Config:    filepath.Join(root, "config.toml"),
		Data:      filepath.Join(root, "data"),
		Artifacts: filepath.Join(root, "data", "artifacts"),
		Agents:    filepath.Join(root, "agents"),
		Cache:     filepath.Join(root, "cache"),
		Logs:      filepath.Join(root, "logs"),
		Database:  filepath.Join(root, "data", "server.db"),
	}

	for _, dir := range []string{layout.Root, layout.Data, layout.Artifacts, layout.Agents, layout.Cache, layout.Logs} {
		if err := ensureDir(dir); err != nil {
			return Layout{}, err
		}
	}
	if err := configfile.Ensure(layout.Config); err != nil {
		return Layout{}, err
	}

	return layout, nil
}

func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".sumi"), nil
}

func ensureDir(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s is not a safe directory", path)
		}
		return secureDirectory(path)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	info, err = os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a safe directory", path)
	}
	return secureDirectory(path)
}

func secureDirectory(path string) error {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	directory := os.NewFile(uintptr(descriptor), path)
	defer directory.Close()
	if err := directory.Chmod(0o700); err != nil {
		return fmt.Errorf("secure %s: %w", path, err)
	}
	info, err := directory.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s is not a safe directory", path)
	}
	return nil
}
