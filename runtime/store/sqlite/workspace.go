package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

const defaultWorkspaceID = "ws_default"

func workspaceID(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return defaultWorkspaceID
	}
	sum := sha256.Sum256([]byte(trimmed))
	return "ws_" + hex.EncodeToString(sum[:])[:12]
}

func workspaceName(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "workspace"
	}
	name := filepath.Base(trimmed)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "workspace"
	}
	return name
}
