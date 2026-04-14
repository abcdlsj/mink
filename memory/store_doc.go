package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func render(doc Doc) string {
	doc.Scope = normalizeScope(doc.Scope)
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", doc.ID)
	if doc.Workspace != "" {
		fmt.Fprintf(&b, "workspace_id: %s\n", escape(doc.Workspace))
	}
	fmt.Fprintf(&b, "scope_kind: %s\n", escape(doc.Scope.Kind))
	if doc.Scope.Key != "" {
		fmt.Fprintf(&b, "scope_key: %s\n", escape(doc.Scope.Key))
	}
	fmt.Fprintf(&b, "title: %s\n", escape(doc.Title))
	fmt.Fprintf(&b, "kind: %s\n", escape(doc.Kind))
	b.WriteString("tags:\n")
	for _, tag := range doc.Tags {
		fmt.Fprintf(&b, "  - %s\n", escape(tag))
	}
	if doc.TaskID != "" {
		fmt.Fprintf(&b, "task_id: %s\n", doc.TaskID)
	}
	if doc.RunID != "" {
		fmt.Fprintf(&b, "run_id: %s\n", doc.RunID)
	}
	if doc.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", escape(doc.Source))
	}
	if doc.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", escape(doc.Summary))
	}
	fmt.Fprintf(&b, "updated_at: %s\n", doc.UpdatedAt.UTC().Format(time.RFC3339Nano))
	b.WriteString("---\n\n")
	b.WriteString(doc.Body)
	if !strings.HasSuffix(doc.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func (s *Store) loadPath(path string) (Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	doc := Doc{
		ID:        strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Workspace: s.workspaceID(),
		Scope:     ScopeFromPath(s.scopeRoot(), path),
		Title:     strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Kind:      "note",
		Body:      strings.TrimSpace(string(data)),
	}
	if info, err := os.Stat(path); err == nil {
		doc.UpdatedAt = info.ModTime().UTC()
	}
	if strings.HasPrefix(string(data), "---\n") {
		rest := string(data[4:])
		if i := strings.Index(rest, "\n---\n"); i >= 0 {
			parseFrontmatter(&doc, rest[:i])
			doc.Body = strings.TrimSpace(rest[i+5:])
		}
	}
	doc.Title = nonEmpty(strings.TrimSpace(doc.Title), doc.ID)
	doc.Scope = normalizeScope(doc.Scope)
	doc.Kind = nonEmpty(strings.TrimSpace(doc.Kind), "note")
	if doc.Body == "" {
		return Doc{}, fmt.Errorf("memory: empty body")
	}
	return doc, nil
}

func joinTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return strings.Join(tags, ",")
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseFrontmatter(doc *Doc, head string) {
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
				doc.Tags = append(doc.Tags, unquote(strings.TrimSpace(strings.TrimPrefix(next, "- "))))
				i++
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = unquote(strings.TrimSpace(val))
		switch strings.TrimSpace(key) {
		case "id":
			doc.ID = nonEmpty(val, doc.ID)
		case "workspace_id":
			doc.Workspace = val
		case "scope_kind":
			doc.Scope.Kind = val
		case "scope_key":
			doc.Scope.Key = val
		case "title":
			doc.Title = val
		case "kind":
			doc.Kind = val
		case "task_id":
			doc.TaskID = val
		case "run_id":
			doc.RunID = val
		case "source":
			doc.Source = val
		case "summary":
			doc.Summary = val
		case "updated_at":
			if ts, err := time.Parse(time.RFC3339Nano, val); err == nil {
				doc.UpdatedAt = ts
			}
		}
	}
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if strings.ContainsAny(s, ":#[]{}\",'") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
	}
	return s
}
