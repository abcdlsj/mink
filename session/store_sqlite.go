package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/internal/xstr"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

type SQLiteStore struct {
	db            *rtsqlite.DB
	workspaceID   string
	workspacePath string
	workspaceName string
}

func NewSQLiteStore(db *rtsqlite.DB, workspacePath string) *SQLiteStore {
	ws := rtsqlite.ResolveWorkspace(workspacePath)
	return &SQLiteStore{
		db:            db,
		workspaceID:   ws.ID,
		workspacePath: ws.Path,
		workspaceName: ws.Name,
	}
}

func (s *SQLiteStore) errNil() error {
	return fmt.Errorf("session: nil sqlite store")
}

func snapshotTitle(snap *Snapshot) string {
	if snap == nil {
		return ""
	}
	for _, entry := range snap.Entries {
		if entry.Message.Role == "user" {
			text := strings.TrimSpace(entry.Message.Content)
			if text != "" {
				return xstr.Truncate(text, 48)
			}
		}
	}
	return snap.ID
}

func snapshotMeta(snap *Snapshot) *Snapshot {
	if snap == nil {
		return nil
	}
	meta := *snap
	meta.Entries = nil
	return &meta
}

func snapshotMetadataJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

var firstNonEmpty = xstr.FirstNonEmpty
