package config

import (
	"os"
	"path/filepath"
	"strings"
)

func (c Config) DataRoot() string {
	if strings.TrimSpace(c.DataDir) != "" {
		return strings.TrimSpace(c.DataDir)
	}
	return DefaultDataDir()
}

func (c Config) MemoryDir() string {
	return filepath.Join(c.DataRoot(), "memory")
}

func (c Config) CronPath() string {
	return filepath.Join(c.DataRoot(), "cron", "tasks.json")
}

func (c Config) PermissionsPath() string {
	return filepath.Join(c.DataRoot(), "state", "permissions.json")
}

func (c Config) CollabTeamsPath() string {
	return filepath.Join(c.DataRoot(), "collab", "teams.json")
}

func (c Config) ResolvedSoulPath() string {
	if path := strings.TrimSpace(c.SoulPath); path != "" {
		return path
	}
	return filepath.Join(c.DataRoot(), "SOUL.md")
}

func ConfigPath() string {
	return filepath.Join(DefaultDataDir(), "config.toml")
}

func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".sumi"
	}
	return filepath.Join(home, ".sumi")
}
