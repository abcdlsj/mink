package cli

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type completionKind int

const (
	completionCommand completionKind = iota
	completionFile
)

type completionHint struct {
	Kind  completionKind
	Value string
	Desc  string
}

func (m *shellModel) refreshSuggestions() {
	if m.focus != focusComposer || m.overlay != overlayNone || m.app == nil {
		m.clearSuggestions()
		return
	}
	if m.refreshCommandSuggestions() || m.refreshFileSuggestions() {
		m.clampSuggestion()
		return
	}
	m.clearSuggestions()
}

func (m *shellModel) refreshCommandSuggestions() bool {
	query, ok := commandQuery(m.input.Value())
	if !ok {
		return false
	}
	var out []completionHint
	for _, c := range m.app.Commands() {
		name := strings.TrimSpace(c.Name())
		if name == "" || !strings.HasPrefix(name, query) {
			continue
		}
		out = append(out, completionHint{Kind: completionCommand, Value: name, Desc: strings.TrimSpace(c.Desc())})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Value < out[j].Value
	})
	m.suggests = out
	return true
}

func (m *shellModel) refreshFileSuggestions() bool {
	head, query, ok := fileQuery(m.input.Value())
	if !ok {
		return false
	}
	if !m.filesOK {
		m.files = listWorkspaceFiles(m.app.Workspace())
		m.filesOK = true
	}
	var out []completionHint
	q := strings.ToLower(query)
	for _, path := range m.files {
		if !matchFile(path, q) {
			continue
		}
		out = append(out, completionHint{Kind: completionFile, Value: path, Desc: head})
		if len(out) >= 40 {
			break
		}
	}
	m.suggests = out
	return true
}

func (m *shellModel) clearSuggestions() {
	m.suggests = nil
	m.suggestRows = 0
	m.suggest = 0
}

func (m *shellModel) clampSuggestion() {
	if len(m.suggests) == 0 {
		m.suggest = 0
		return
	}
	if m.suggest < 0 {
		m.suggest = 0
	}
	if m.suggest >= len(m.suggests) {
		m.suggest = len(m.suggests) - 1
	}
}

func commandQuery(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return "", false
	}
	raw := strings.TrimPrefix(input, "/")
	if strings.ContainsAny(raw, " \t\r\n") {
		return "", false
	}
	return raw, true
}

func fileQuery(input string) (head, query string, ok bool) {
	input = strings.TrimRight(input, " \t\r\n")
	i := strings.LastIndexByte(input, '@')
	if i < 0 {
		return "", "", false
	}
	tail := input[i+1:]
	if strings.ContainsAny(tail, " \t\r\n") {
		return "", "", false
	}
	return input[:i], tail, true
}

func (m *shellModel) moveSuggestion(delta int) {
	if len(m.suggests) == 0 {
		return
	}
	m.suggest += delta
	if m.suggest < 0 {
		m.suggest = len(m.suggests) - 1
	}
	if m.suggest >= len(m.suggests) {
		m.suggest = 0
	}
}

func (m *shellModel) acceptSuggestion() {
	if len(m.suggests) == 0 {
		return
	}
	h := m.suggests[m.suggest]
	switch h.Kind {
	case completionCommand:
		m.input.SetValue("/" + h.Value + " ")
	case completionFile:
		head, _, _ := fileQuery(m.input.Value())
		m.input.SetValue(head + "@" + h.Value + " ")
	}
	m.input.CursorEnd()
	m.clearSuggestions()
}

func (m *shellModel) exactSuggestion() bool {
	if len(m.suggests) == 0 || m.suggests[m.suggest].Kind != completionCommand {
		return false
	}
	query, ok := commandQuery(m.input.Value())
	return ok && query == m.suggests[m.suggest].Value
}

func listWorkspaceFiles(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipFileDir(name) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFileName(name) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}

func skipFileDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".idea", ".vscode", "node_modules", "vendor", "dist", "build", "target", ".next", ".turbo":
		return true
	default:
		return false
	}
}

func skipFileName(name string) bool {
	switch name {
	case ".DS_Store":
		return true
	default:
		return strings.HasSuffix(name, ".lock")
	}
}

func matchFile(path, q string) bool {
	if q == "" {
		return true
	}
	path = strings.ToLower(path)
	if strings.Contains(path, q) {
		return true
	}
	j := 0
	for i := 0; i < len(path) && j < len(q); i++ {
		if path[i] == q[j] {
			j++
		}
	}
	return j == len(q)
}
