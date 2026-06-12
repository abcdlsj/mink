package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/sumi/space"
)

func (s *Store) SaveSpace(sp *space.Space) error {
	if sp == nil {
		return fmt.Errorf("space is nil")
	}
	if strings.TrimSpace(sp.ID) == "" {
		return fmt.Errorf("space id is empty")
	}
	data, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.spacePath(sp)
	return writeFile(path, append(data, '\n'))
}

func (s *Store) LoadSpace(id string) (*space.Space, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("space id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, ok := s.findSpacePathLocked(id)
	if !ok {
		return nil, fmt.Errorf("space not found: %s", id)
	}
	return readSpaceFile(path)
}

func (s *Store) ListSpaces() ([]*space.Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := filepath.Clean(s.spacesRoot)
	out := make([]*space.Space, 0)
	err := walkDirJSON(root, func(path string) error {
		sp, err := readSpaceFile(path)
		if err != nil {
			return nil
		}
		out = append(out, sp)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) FindSpaceByKindAndSeed(kind space.Kind, seed string) (*space.Space, error) {
	all, err := s.ListSpaces()
	if err != nil {
		return nil, err
	}
	for _, sp := range all {
		if sp.Kind == kind && sp.Title == seed {
			return sp, nil
		}
	}
	return nil, nil
}

func (s *Store) DeleteSpace(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, ok := s.findSpacePathLocked(id)
	if !ok {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) spacePath(sp *space.Space) string {
	kind := strings.TrimSpace(string(sp.Kind))
	if kind == "" {
		kind = "other"
	}
	return filepath.Join(s.spacesRoot, kind, sp.ID+".json")
}

func (s *Store) findSpacePathLocked(id string) (string, bool) {
	root := filepath.Clean(s.spacesRoot)
	var hit string
	_ = walkDirJSON(root, func(path string) error {
		if strings.HasSuffix(path, string(filepath.Separator)+id+".json") {
			hit = path
		}
		return nil
	})
	return hit, hit != ""
}

func readSpaceFile(path string) (*space.Space, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var sp space.Space
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, err
	}
	return &sp, nil
}

func walkDirJSON(root string, fn func(path string) error) error {
	if !dirExists(root) {
		return nil
	}
	return filepathWalk(root, func(path string, isDir bool) error {
		if isDir {
			return nil
		}
		if !strings.HasSuffix(path, ".json") {
			return nil
		}
		return fn(path)
	})
}
