package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/space"
)

// SaveSpaceUnderDeliveryFence is the write-side authority seam for routed
// worker finalizes. It performs, inside a SINGLE Store-mutex critical section:
//
//  1. load the authoritative Delivery record,
//  2. verify the presented fence (ownerID, version) still owns the LIVE lease
//     as of now (delivery.OwnsLiveLease),
//  3. only if so, persist sp; otherwise return space.ErrStaleDeliveryWrite and
//     write no bytes at all.
//
// Because the fence check and the Space byte-write share one s.mu acquisition,
// a concurrent Claim (which serializes on the same s.mu and bumps the lease
// version) can never interleave between them. This is the linearization point
// Iris required: a superseded worker that reaches here after a newer owner has
// claimed is rejected BEFORE any content lands on disk — not merely reconciled
// away afterward. The fence is passed as raw (ownerID, version) so the space
// package need not import delivery; the Delivery domain type is rebuilt here.
func (s *Store) SaveSpaceUnderDeliveryFence(deliveryID, fenceOwnerID string, fenceVersion int64, now time.Time, sp *space.Space) error {
	if sp == nil {
		return fmt.Errorf("space is nil")
	}
	if strings.TrimSpace(sp.ID) == "" {
		return fmt.Errorf("space id is empty")
	}
	deliveryID = strings.TrimSpace(deliveryID)
	if deliveryID == "" {
		return fmt.Errorf("delivery id is empty")
	}
	data, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return err
	}
	fence := delivery.Fence{OwnerID: fenceOwnerID, Version: fenceVersion}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.Deliveries().loadLocked(deliveryID)
	if err != nil {
		return err
	}
	if d == nil {
		return delivery.ErrNotFound
	}
	if !d.OwnsLiveLease(fence, now) {
		return space.ErrStaleDeliveryWrite
	}
	path := s.spacePath(sp)
	return writeFile(path, append(data, '\n'))
}

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

func (s *Store) FindSpaceByKindAndKey(kind space.Kind, key string) (*space.Space, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	all, err := s.ListSpaces()
	if err != nil {
		return nil, err
	}
	for _, sp := range all {
		if sp.Kind == kind && sp.Key == key {
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
