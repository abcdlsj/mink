package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/sumi/space"
)

// SaveSpace serializes the Space to ~/.sumi/spaces/<kind>/<id>.json.
// It is safe to call concurrently with SaveSession; the two paths
// never share files.
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

// LoadSpace reads back a Space written by SaveSpace.
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

// ListSpaces walks the spaces directory and returns every readable
// Space. It does not validate kind partitioning beyond directory
// layout.
func (s *Store) ListSpaces() ([]*space.Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := filepath.Clean(s.spacesRoot)
	out := make([]*space.Space, 0)
	err := walkDirJSON(root, func(path string) error {
		sp, err := readSpaceFile(path)
		if err != nil {
			return nil // tolerate per-file corruption; logs go to caller
		}
		out = append(out, sp)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FindSpaceByKindAndSeed looks for an existing Space whose kind +
// title-derived seed matches. It is used by the manager (P1.3) to
// pick the singleton channel / agent_dm rather than minting a new
// one each turn.
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

// walkDirJSON visits every *.json regular file under root. Missing
// roots are treated as empty.
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
