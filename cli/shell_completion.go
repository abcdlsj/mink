package cli

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const fileSuggestionLimit = 2000

type completionKind int

const (
	completionCommand completionKind = iota
	completionFile
	completionPersona
)

type completionHint struct {
	Kind  completionKind
	Value string
	Desc  string
}

type shellFilesLoadedMsg struct {
	Root  string
	Files []string
}

func (m *shellModel) refreshSuggestions() tea.Cmd {
	if m.focus != focusComposer || m.overlay != overlayNone || m.app == nil {
		m.clearSuggestions()
		return nil
	}
	if m.refreshCommandSuggestions() {
		m.clampSuggestion()
		return nil
	}
	if m.refreshPersonaSuggestions() {
		m.clampSuggestion()
		return nil
	}
	cmd := m.refreshFileSuggestions()
	if m.suggests != nil {
		m.clampSuggestion()
	} else if cmd == nil {
		m.clearSuggestions()
	}
	return cmd
}

func (m *shellModel) refreshPersonaSuggestions() bool {
	_, query, ok := personaQuery(m.input.Value())
	if !ok {
		return false
	}
	reg := m.app.Personas()
	if reg == nil {
		m.suggests = nil
		return true
	}
	q := strings.ToLower(query)
	var out []completionHint
	for _, p := range reg.List() {
		if q != "" && !strings.Contains(strings.ToLower(p.ID), q) {
			continue
		}
		desc := p.Display
		if desc == "" {
			desc = "persona"
		}
		out = append(out, completionHint{Kind: completionPersona, Value: p.ID, Desc: desc})
	}
	m.suggests = out
	return true
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

func (m *shellModel) refreshFileSuggestions() tea.Cmd {
	head, query, ok := fileQuery(m.input.Value())
	if !ok {
		return nil
	}
	root := m.app.Workspace()
	if !m.filesOK && !m.filesLoading {
		m.filesLoading = true
		return loadWorkspaceFilesCmd(root)
	}
	if !m.filesOK {
		return nil
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
	return nil
}

func loadWorkspaceFilesCmd(root string) tea.Cmd {
	return func() tea.Msg {
		return shellFilesLoadedMsg{Root: root, Files: listWorkspaceFiles(root)}
	}
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
	if strings.HasPrefix(tail, "persona:") {
		return "", "", false
	}
	return input[:i], tail, true
}

func personaQuery(input string) (head, query string, ok bool) {
	input = strings.TrimRight(input, " \t\r\n")
	i := strings.LastIndexByte(input, '@')
	if i < 0 {
		return "", "", false
	}
	tail := input[i+1:]
	if !strings.HasPrefix(tail, "persona:") {
		return "", "", false
	}
	q := strings.TrimPrefix(tail, "persona:")
	if strings.ContainsAny(q, " \t\r\n") {
		return "", "", false
	}
	return input[:i], q, true
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
	case completionPersona:
		head, _, _ := personaQuery(m.input.Value())
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
			if path != root && skipFileDir(name) {
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
		if len(out) >= fileSuggestionLimit {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func skipFileDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target",
		"Library", "Applications", "Downloads", "Music", "Movies", "Pictures", "Public",
		"go", "venv", ".venv", "__pycache__":
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
