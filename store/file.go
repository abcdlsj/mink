package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Store struct {
	path    string
	current string
	runlog  string
	mu      sync.Mutex
}

func Open(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	s := &Store{
		path:    filepath.Join(root, "sessions.jsonl"),
		current: filepath.Join(root, "state", "current_sessions.json"),
		runlog:  filepath.Join(root, "runlog.jsonl"),
	}
	if err := s.ensurePaths(root); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ensurePaths(root string) error {
	for _, dir := range []string{root, filepath.Dir(s.current)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := touch(s.path); err != nil {
		return err
	}
	return touch(s.runlog)
}

func (s *Store) Close() error { return nil }

func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func trimID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("id is empty")
	}
	return id, nil
}
