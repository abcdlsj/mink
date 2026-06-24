package memory

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *store) put(ctx context.Context, sc scope, d doc) (doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d = s.prepareDoc(sc, d)
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		return doc{}, err
	}
	if err := writeFile(d.Path, []byte(renderDoc(d))); err != nil {
		return doc{}, err
	}
	return d, nil
}

func (s *store) prepareDoc(sc scope, d doc) doc {
	if strings.TrimSpace(d.ID) == "" {
		d.ID = fmt.Sprintf("mem-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	d.ScopeKind = sc.Kind
	d.ScopeKey = sc.Key
	if strings.TrimSpace(d.Kind) == "" {
		d.Kind = "note"
	}
	if strings.TrimSpace(d.Summary) == "" {
		d.Summary = summarize(d.Body, 140)
	}
	if strings.TrimSpace(d.Confidence) == "" {
		d.Confidence = "medium"
	}
	d.ID = docFileID(d.Title, d.ID)
	d.Path = filepath.Join(scopeDir(s.root, sc), d.ID+".md")
	return d
}

func docFileID(title, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = fmt.Sprintf("mem-%d", time.Now().UnixNano())
	}
	if titleSlug := slug(title, ""); titleSlug != "" && !strings.Contains(titleSlug, id) {
		return titleSlug + "-" + id
	}
	return slug(id, id)
}

func (s *store) recent(ctx context.Context, sc scope, limit int) ([]doc, error) {
	docs, err := s.loadScope(sc)
	if err != nil {
		return nil, err
	}
	sortDocs(docs)
	if len(docs) > limit {
		docs = docs[:limit]
	}
	return docs, nil
}

func (s *store) search(ctx context.Context, scopes []scope, q string, limit int) ([]doc, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	var out []doc
	seen := map[string]bool{}
	for _, sc := range scopes {
		docs, err := s.loadScope(sc)
		if err != nil {
			return nil, err
		}
		sortDocs(docs)
		for _, d := range docs {
			if seen[d.Path] || !matchesQuery(d, q) {
				continue
			}
			seen[d.Path] = true
			out = append(out, d)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (s *store) delete(ctx context.Context, sc scope, id string) (doc, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return doc{}, fmt.Errorf("memory id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	docs, err := s.loadScopeLocked(sc)
	if err != nil {
		return doc{}, err
	}
	for _, d := range docs {
		if d.ID == id || strings.TrimSuffix(filepath.Base(d.Path), filepath.Ext(d.Path)) == id {
			if err := os.Remove(d.Path); err != nil {
				return doc{}, err
			}
			return d, nil
		}
	}
	return doc{}, fmt.Errorf("memory %s not found in %s", id, scopeText(sc))
}

func (s *store) update(ctx context.Context, sc scope, in updateArgs) (doc, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return doc{}, fmt.Errorf("memory id is required")
	}
	title := strings.TrimSpace(in.Title)
	body := strings.TrimSpace(in.Body)
	if title == "" || body == "" {
		return doc{}, fmt.Errorf("title and body are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	docs, err := s.loadScopeLocked(sc)
	if err != nil {
		return doc{}, err
	}
	for _, d := range docs {
		if d.ID != id && strings.TrimSuffix(filepath.Base(d.Path), filepath.Ext(d.Path)) != id {
			continue
		}
		d.Title = title
		d.Body = body
		d.Summary = blank(in.Summary, summarize(body, 140))
		d.Kind = blank(in.Kind, "note")
		d.Confidence = normalizeConfidence(in.Confidence)
		d.UpdatedAt = time.Now().UTC()
		d.ScopeKind = sc.Kind
		d.ScopeKey = sc.Key
		if err := writeFile(d.Path, []byte(renderDoc(d))); err != nil {
			return doc{}, err
		}
		return d, nil
	}
	return doc{}, fmt.Errorf("memory %s not found in %s", id, scopeText(sc))
}

func matchesQuery(d doc, q string) bool {
	text := strings.ToLower(strings.Join([]string{d.Title, d.Summary, d.Body}, "\n"))
	return strings.Contains(text, q)
}

func sortDocs(docs []doc) {
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
	})
}

func (s *store) loadScope(sc scope) ([]doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadScopeLocked(sc)
}

func (s *store) loadScopeLocked(sc scope) ([]doc, error) {
	root := scopeDir(s.root, sc)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []doc
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		doc, err := loadDoc(path, sc)
		if err != nil {
			return err
		}
		out = append(out, doc)
		return nil
	})
	return out, err
}

func loadDoc(path string, sc scope) (doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return doc{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return doc{}, err
	}
	body := string(data)
	head := splitFrontmatter(&body)
	d := doc{
		ID:        strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		ScopeKind: sc.Kind,
		ScopeKey:  sc.Key,
		Path:      path,
		Body:      strings.TrimSpace(body),
		CreatedAt: info.ModTime().UTC(),
		UpdatedAt: info.ModTime().UTC(),
		Kind:      "note",
	}
	parseFrontmatter(&d, head)
	fillDocDefaults(&d)
	return d, nil
}

func splitFrontmatter(body *string) string {
	if !strings.HasPrefix(*body, "---\n") {
		return ""
	}
	rest := (*body)[4:]
	i := strings.Index(rest, "\n---\n")
	if i < 0 {
		return ""
	}
	*body = rest[i+5:]
	return rest[:i]
}

func fillDocDefaults(d *doc) {
	if d.Title == "" {
		d.Title = firstHeading(d.Body)
	}
	if d.Title == "" {
		d.Title = strings.TrimSuffix(filepath.Base(d.Path), filepath.Ext(d.Path))
	}
	if d.Summary == "" {
		d.Summary = summarize(d.Body, 160)
	}
	if d.Confidence == "" {
		d.Confidence = "medium"
	}
}

func renderDoc(d doc) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "scope_kind: %s\n", quote(d.ScopeKind))
	fmt.Fprintf(&b, "scope_key: %s\n", quote(d.ScopeKey))
	fmt.Fprintf(&b, "title: %s\n", quote(d.Title))
	fmt.Fprintf(&b, "kind: %s\n", quote(d.Kind))
	if d.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", quote(d.Source))
	}
	if d.SourceSpaceID != "" {
		fmt.Fprintf(&b, "source_space_id: %s\n", quote(d.SourceSpaceID))
	}
	if d.SourceMessageID != "" {
		fmt.Fprintf(&b, "source_message_id: %s\n", quote(d.SourceMessageID))
	}
	if d.CreatedBy != "" {
		fmt.Fprintf(&b, "created_by: %s\n", quote(d.CreatedBy))
	}
	if d.Confidence != "" {
		fmt.Fprintf(&b, "confidence: %s\n", quote(d.Confidence))
	}
	if d.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", quote(d.Summary))
	}
	if len(d.Tags) > 0 {
		b.WriteString("tags:\n")
		for _, tag := range d.Tags {
			fmt.Fprintf(&b, "  - %s\n", quote(tag))
		}
	}
	if !d.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created_at: %s\n", d.CreatedAt.Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", d.UpdatedAt.Format(time.RFC3339Nano))
	if !d.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "expires_at: %s\n", d.ExpiresAt.Format(time.RFC3339Nano))
	}
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(strings.TrimSpace(d.Title))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(d.Body))
	if !strings.HasSuffix(d.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func parseFrontmatter(d *doc, head string) {
	if strings.TrimSpace(head) == "" {
		return
	}
	lines := strings.Split(head, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if line == "tags:" {
			for i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(next, "- ") {
					break
				}
				d.Tags = append(d.Tags, unquote(strings.TrimSpace(strings.TrimPrefix(next, "- "))))
				i++
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		d.applyMeta(strings.TrimSpace(key), unquote(strings.TrimSpace(val)))
	}
}

func (d *doc) applyMeta(key, val string) {
	switch key {
	case "scope_kind":
		d.ScopeKind = val
	case "scope_key":
		d.ScopeKey = val
	case "title":
		d.Title = val
	case "kind":
		d.Kind = val
	case "source":
		d.Source = val
	case "source_space_id":
		d.SourceSpaceID = val
	case "source_message_id":
		d.SourceMessageID = val
	case "created_by":
		d.CreatedBy = val
	case "confidence":
		d.Confidence = val
	case "summary":
		d.Summary = val
	case "created_at":
		if ts, err := time.Parse(time.RFC3339Nano, val); err == nil {
			d.CreatedAt = ts
		}
	case "updated_at":
		if ts, err := time.Parse(time.RFC3339Nano, val); err == nil {
			d.UpdatedAt = ts
		}
	case "expires_at":
		if ts, err := time.Parse(time.RFC3339Nano, val); err == nil {
			d.ExpiresAt = ts
		}
	}
}

func firstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

func quote(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	return strconv.Quote(s)
}

func unquote(s string) string {
	if v, err := strconv.Unquote(s); err == nil {
		return v
	}
	return s
}

func writeFile(path string, data []byte) error {
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
