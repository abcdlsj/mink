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
	ID            string
	Display       string
	Runtime       string
	Model         string
	Description   string
	Tools         []string
	Capabilities  []string
	TaskPolicy    string
	ShowInSidebar bool
	SoulPath      string
	Root          string
}

type Meta struct {
	Display       string   `toml:"display"`
	Runtime       string   `toml:"runtime"`
	Model         string   `toml:"model"`
	Description   string   `toml:"description"`
	Tools         []string `toml:"tools"`
	Capabilities  []string `toml:"capabilities"`
	TaskPolicy    string   `toml:"task_policy"`
	ShowInSidebar *bool    `toml:"show_in_sidebar"`
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
		ID:            sanitizeID(id),
		Display:       blank(m.Display, id),
		Runtime:       strings.TrimSpace(m.Runtime),
		Model:         strings.TrimSpace(m.Model),
		Description:   strings.TrimSpace(m.Description),
		Tools:         cloneStrings(m.Tools),
		Capabilities:  NormalizeCapabilities(m.Capabilities),
		TaskPolicy:    normalizeTaskPolicy(m.TaskPolicy),
		ShowInSidebar: true,
		Root:          dir,
	}
	if m.ShowInSidebar != nil {
		p.ShowInSidebar = *m.ShowInSidebar
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

func normalizeTaskPolicy(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto_commit", "auto-commit", "autocommit":
		return "auto_commit"
	default:
		return "propose_only"
	}
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

func NormalizeCapabilities(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = NormalizeCapability(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func NormalizeCapability(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", ".")
	s = strings.ReplaceAll(s, ":", ".")
	switch s {
	case "plan":
		return "task.plan"
	case "create":
		return "task.create"
	case "assign":
		return "task.assign"
	case "execute", "exec":
		return "task.execute"
	case "review":
		return "task.review"
	default:
		return s
	}
}

func (p *Persona) HasCapability(cap string) bool {
	want := NormalizeCapability(cap)
	if p == nil || want == "" {
		return false
	}
	for _, got := range p.Capabilities {
		if NormalizeCapability(got) == want {
			return true
		}
	}
	return false
}

func blank(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return strings.TrimSpace(fallback)
	}
	return s
}
