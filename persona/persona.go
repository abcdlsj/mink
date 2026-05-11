package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

type Persona struct {
	ID          string
	Display     string
	Runtime     string
	Description string
	Tools       []string
	SoulPath    string
	Root        string
}

type Meta struct {
	Display     string   `toml:"display"`
	Runtime     string   `toml:"runtime"`
	Description string   `toml:"description"`
	Tools       []string `toml:"tools"`
}

type Registry struct {
	root string
	mu   sync.RWMutex
	all  map[string]*Persona
}

func NewRegistry(root string) *Registry {
	return &Registry{root: root, all: map[string]*Persona{}}
}

func (r *Registry) Root() string { return r.root }

func (r *Registry) Load() error {
	if strings.TrimSpace(r.root) == "" {
		return nil
	}
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return err
	}
	all := map[string]*Persona{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := loadDir(r.root, e.Name())
		if err != nil {
			return fmt.Errorf("persona %s: %w", e.Name(), err)
		}
		if p == nil {
			continue
		}
		all[p.ID] = p
	}
	r.mu.Lock()
	r.all = all
	r.mu.Unlock()
	return nil
}

func (r *Registry) Get(id string) *Persona {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.all[id]
}

func (r *Registry) List() []*Persona {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Persona, 0, len(r.all))
	for _, p := range r.all {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Create(id string, meta Meta, soul string) (*Persona, error) {
	id = sanitizeID(id)
	if id == "" {
		return nil, fmt.Errorf("persona id required")
	}
	dir := filepath.Join(r.root, id)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("persona %s exists", id)
	}
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		return nil, err
	}
	metaBytes, err := marshalMeta(meta)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.toml"), metaBytes, 0o644); err != nil {
		return nil, err
	}
	if strings.TrimSpace(soul) != "" {
		if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte(soul), 0o644); err != nil {
			return nil, err
		}
	}
	if err := r.Load(); err != nil {
		return nil, err
	}
	return r.Get(id), nil
}

func loadDir(root, id string) (*Persona, error) {
	dir := filepath.Join(root, id)
	metaPath := filepath.Join(dir, "meta.toml")
	if _, err := os.Stat(metaPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m Meta
	if _, err := toml.DecodeFile(metaPath, &m); err != nil {
		return nil, err
	}
	p := &Persona{
		ID:          sanitizeID(id),
		Display:     blank(m.Display, id),
		Runtime:     strings.TrimSpace(m.Runtime),
		Description: strings.TrimSpace(m.Description),
		Tools:       cloneStrings(m.Tools),
		Root:        dir,
	}
	if p.ID == "" {
		return nil, fmt.Errorf("invalid persona dir name: %q", id)
	}
	soulPath := filepath.Join(dir, "SOUL.md")
	if _, err := os.Stat(soulPath); err == nil {
		p.SoulPath = soulPath
	}
	return p, nil
}

func marshalMeta(m Meta) ([]byte, error) {
	var b strings.Builder
	enc := toml.NewEncoder(&b)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func blank(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return strings.TrimSpace(fallback)
	}
	return s
}
