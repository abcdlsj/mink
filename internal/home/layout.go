package home

import (
	"fmt"
	"os"
	"path/filepath"
)

type Layout struct {
	Root     string
	Config   string
	Data     string
	Agents   string
	Cache    string
	Logs     string
	Database string
}

func Ensure(root string) (Layout, error) {
	if root == "" {
		return Layout{}, fmt.Errorf("data root is required")
	}

	layout := Layout{
		Root:     root,
		Config:   filepath.Join(root, "config.toml"),
		Data:     filepath.Join(root, "data"),
		Agents:   filepath.Join(root, "agents"),
		Cache:    filepath.Join(root, "cache"),
		Logs:     filepath.Join(root, "logs"),
		Database: filepath.Join(root, "data", "server.db"),
	}

	for _, dir := range []string{layout.Root, layout.Data, layout.Agents, layout.Cache, layout.Logs} {
		if err := ensureDir(dir); err != nil {
			return Layout{}, err
		}
	}
	if err := ensureConfig(layout.Config); err != nil {
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
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure %s: %w", path, err)
	}
	return nil
}

func ensureConfig(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		if _, writeErr := file.WriteString("version = 1\n"); writeErr != nil {
			file.Close()
			return fmt.Errorf("write %s: %w", path, writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close %s: %w", path, closeErr)
		}
		return nil
	}
	if !os.IsExist(err) {
		return fmt.Errorf("create %s: %w", path, err)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return fmt.Errorf("inspect %s: %w", path, statErr)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
		return fmt.Errorf("secure %s: %w", path, chmodErr)
	}
	return nil
}
