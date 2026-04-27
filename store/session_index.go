package store

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/mink/session"
)

type SessionMeta struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Title      string    `json:"title,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Path       string    `json:"path"`
	RunlogPath string    `json:"runlog_path"`
	Messages   int       `json:"messages"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Store) SessionIndex() ([]SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadIndexLocked()
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(idx))
	for _, meta := range idx {
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *Store) loadIndexLocked() (map[string]SessionMeta, error) {
	data, err := os.ReadFile(s.sessionIndex)
	if err != nil {
		if os.IsNotExist(err) {
			return s.rebuildIndexLocked()
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]SessionMeta{}, nil
	}
	var idx map[string]SessionMeta
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	if idx == nil {
		idx = map[string]SessionMeta{}
	}
	return idx, nil
}

func (s *Store) updateIndexLocked(v *session.Session, path string) error {
	idx, err := s.loadIndexLocked()
	if err != nil {
		return err
	}
	idx[v.ID] = s.meta(v, path)
	return s.saveIndexLocked(idx)
}

func (s *Store) rebuildIndexLocked() (map[string]SessionMeta, error) {
	idx := map[string]SessionMeta{}
	err := walkFiles(s.sessionsDir, ".json", func(path string) error {
		d, err := loadSessionFile(path)
		if err != nil {
			return err
		}
		idx[d.ID] = s.meta(fromDisk(d), path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, s.saveIndexLocked(idx)
}

func (s *Store) saveIndexLocked(idx map[string]SessionMeta) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.sessionIndex, append(data, '\n'))
}

func (s *Store) meta(v *session.Session, path string) SessionMeta {
	return SessionMeta{
		ID:         v.ID,
		Source:     v.Source,
		Title:      v.Title,
		Summary:    v.Summary,
		Path:       path,
		RunlogPath: s.runlogPath(v.ID, v.Source, v.CreatedAt),
		Messages:   len(v.Messages),
		CreatedAt:  v.CreatedAt,
		UpdatedAt:  v.UpdatedAt,
	}
}

func latestBySource(idx map[string]SessionMeta, source string) string {
	source = strings.TrimSpace(source)
	var cur SessionMeta
	for _, meta := range idx {
		if strings.TrimSpace(meta.Source) != source {
			continue
		}
		if cur.ID == "" || meta.UpdatedAt.After(cur.UpdatedAt) {
			cur = meta
		}
	}
	return cur.ID
}
