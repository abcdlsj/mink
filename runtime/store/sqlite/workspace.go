package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abcdlsj/mink/runtime/id"
)

const defaultWorkspaceID = "ws_default"

type WorkspaceRef struct {
	ID   string
	Path string
	Name string
}

type workspaceMarker struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at,omitempty"`
}

func ResolveWorkspace(path string) WorkspaceRef {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return WorkspaceRef{ID: defaultWorkspaceID, Name: "workspace"}
	}
	return WorkspaceRef{
		ID:   workspaceID(trimmed),
		Path: trimmed,
		Name: workspaceName(trimmed),
	}
}

func workspaceID(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return defaultWorkspaceID
	}
	if markerID := workspaceMarkerID(trimmed); markerID != "" {
		return markerID
	}
	return fallbackWorkspaceID(trimmed)
}

func fallbackWorkspaceID(path string) string {
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

func workspaceMarkerID(path string) string {
	marker := filepath.Join(path, ".mink", "workspace.json")
	if raw, err := os.ReadFile(marker); err == nil {
		var meta workspaceMarker
		if json.Unmarshal(raw, &meta) == nil && strings.TrimSpace(meta.ID) != "" {
			return strings.TrimSpace(meta.ID)
		}
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return ""
	}
	meta := workspaceMarker{
		ID:        id.New("ws"),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return ""
	}
	tmp := marker + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return ""
	}
	if err := os.Rename(tmp, marker); err != nil {
		_ = os.Remove(tmp)
		if raw, readErr := os.ReadFile(marker); readErr == nil {
			var existing workspaceMarker
			if json.Unmarshal(raw, &existing) == nil && strings.TrimSpace(existing.ID) != "" {
				return strings.TrimSpace(existing.ID)
			}
		}
		return ""
	}
	return meta.ID
}
