package userdirs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Layout struct {
	StateRoot       string
	Credentials     string
	HumanCredential string
	Runtime         string
}

func Ensure() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, errors.New("resolve user home")
	}
	layout, err := resolve(home, runtime.GOOS, os.Getenv)
	if err != nil {
		return Layout{}, err
	}
	for _, path := range []string{layout.StateRoot, layout.Credentials, layout.Runtime} {
		if err := ensurePrivateDirectory(path); err != nil {
			return Layout{}, err
		}
	}
	return layout, nil
}

func resolve(home, goos string, getenv func(string) string) (Layout, error) {
	if home == "" || !filepath.IsAbs(home) {
		return Layout{}, errors.New("user home is invalid")
	}
	var stateRoot, runtimeRoot string
	switch goos {
	case "darwin":
		stateRoot = filepath.Join(home, "Library", "Application Support", "Sumi")
		runtimeRoot = filepath.Join(home, "Library", "Caches", "Sumi", "runtime")
	case "linux":
		stateHome := getenv("XDG_STATE_HOME")
		if stateHome == "" {
			stateHome = filepath.Join(home, ".local", "state")
		}
		if !filepath.IsAbs(stateHome) {
			return Layout{}, errors.New("XDG_STATE_HOME must be absolute")
		}
		stateRoot = filepath.Join(stateHome, "sumi")
		runtimeHome := getenv("XDG_RUNTIME_DIR")
		if runtimeHome == "" {
			runtimeRoot = filepath.Join(stateRoot, "runtime")
		} else {
			if !filepath.IsAbs(runtimeHome) {
				return Layout{}, errors.New("XDG_RUNTIME_DIR must be absolute")
			}
			runtimeRoot = filepath.Join(runtimeHome, "sumi")
		}
	default:
		return Layout{}, fmt.Errorf("unsupported operating system %q", goos)
	}
	credentials := filepath.Join(stateRoot, "credentials")
	return Layout{
		StateRoot: stateRoot, Credentials: credentials,
		HumanCredential: filepath.Join(credentials, "human.key"), Runtime: runtimeRoot,
	}, nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return errors.New("sumi user directory is unsafe")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect Sumi user directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("create Sumi user directory")
	}
	info, err = os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("sumi user directory is unsafe")
	}
	return nil
}
