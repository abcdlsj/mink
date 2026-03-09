package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Store interface {
	Load(id string) (*Snapshot, error)
	Save(id string, snap *Snapshot) error
	Delete(id string) error
	List() ([]string, error)
}

type FileStore struct {
	dir string
}

func NewFileStore(dir string) *FileStore {
	os.MkdirAll(dir, 0755)
	return &FileStore{dir: dir}
}

func (s *FileStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *FileStore) Load(id string) (*Snapshot, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *FileStore) Save(id string, snap *Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(id), data, 0644)
}

func (s *FileStore) Delete(id string) error {
	return os.Remove(s.path(id))
}

func (s *FileStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids, nil
}
