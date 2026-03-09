package mink

import (
	"os"
	"path/filepath"
)

func DefaultSockPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mink", "mink.sock")
}

func DefaultPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mink", "mink.pid")
}
