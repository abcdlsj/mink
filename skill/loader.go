package skill

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const skillFile = "SKILL.md"

type Skill struct {
	Name        string
	Desc        string
	When        string
	Risk        string
	Env         []string
	Entrypoints []string
	Examples    []string
	Body        string
	Path        string
}

type Loader struct {
	workspace string
}

func NewLoader(ws string) *Loader {
	return &Loader{workspace: ws}
}

func (l *Loader) Discover() []*Skill {
	byName := make(map[string]*Skill)

	roots := []string{
		l.projectPath(),
		l.globalPath(),
	}

	for _, root := range roots {
		for _, s := range l.discoverIn(root) {
			key := strings.ToLower(s.Name)
			if _, ok := byName[key]; !ok {
				byName[key] = s
			}
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	r := make([]*Skill, 0, len(names))
	for _, name := range names {
		r = append(r, byName[name])
	}
	return r
}

func (l *Loader) Load(name string) *Skill {
	key := strings.ToLower(name)
	for _, s := range l.Discover() {
		if strings.ToLower(s.Name) == key {
			return l.loadBody(s)
		}
	}
	return nil
}

func (l *Loader) discoverIn(root string) []*Skill {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var skills []*Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), skillFile)
		s := l.parseSkill(path)
		if s != nil {
			skills = append(skills, s)
		}
	}
	return skills
}

func (l *Loader) parseSkill(path string) *Skill {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	card := l.parseFrontmatter(string(data))
	name := card.Name
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	card.Name = name
	card.Path = path

	return &card
}

func (l *Loader) loadBody(s *Skill) *Skill {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return s
	}
	s.Body = string(data)
	return s
}

func (l *Loader) Cards() []string {
	skills := l.Discover()
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		if card := s.Card(); card != "" {
			out = append(out, card)
		}
	}
	return out
}

func (s *Skill) Card() string {
	if s == nil {
		return ""
	}
	var lines []string
	lines = append(lines, "- "+s.Name+": "+s.Desc)
	if s.When != "" {
		lines = append(lines, "  when: "+s.When)
	}
	if s.Risk != "" {
		lines = append(lines, "  risk: "+s.Risk)
	}
	if len(s.Env) > 0 {
		lines = append(lines, "  env: "+strings.Join(s.Env, ", "))
	}
	if len(s.Entrypoints) > 0 {
		lines = append(lines, "  entrypoints: "+strings.Join(s.Entrypoints, ", "))
	}
	if len(s.Examples) > 0 {
		lines = append(lines, "  examples: "+strings.Join(s.Examples, " | "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (l *Loader) parseFrontmatter(content string) Skill {
	var s Skill
	if !strings.HasPrefix(content, "---") {
		return s
	}

	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return s
	}

	lines := strings.Split(content[3:end+3], "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch key {
		case "name":
			s.Name = val
		case "description":
			s.Desc = val
		case "when_to_use", "when":
			s.When = val
		case "risk":
			s.Risk = val
		case "env":
			s.Env = splitVals(val)
		case "entrypoints", "entrypoint":
			s.Entrypoints = splitVals(val)
		case "examples", "example":
			s.Examples = splitVals(val)
		}
	}
	return s
}

func splitVals(s string) []string {
	s = strings.TrimSpace(strings.Trim(s, "[]"))
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (l *Loader) projectPath() string {
	return filepath.Join(l.workspace, ".sumi", "skills")
}

func (l *Loader) globalPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sumi", "skills")
}
