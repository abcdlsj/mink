package skill

import (
	"os"
	"path/filepath"
	"strings"
)

const skillFile = "SKILL.md"

type Skill struct {
	Name string
	Desc string
	Body string
	Path string
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

	r := make([]*Skill, 0, len(byName))
	for _, s := range byName {
		r = append(r, s)
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

	name, desc := l.parseFrontmatter(string(data))
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}

	return &Skill{
		Name: name,
		Desc: desc,
		Path: path,
	}
}

func (l *Loader) loadBody(s *Skill) *Skill {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return s
	}
	s.Body = string(data)
	return s
}

func (l *Loader) parseFrontmatter(content string) (name, desc string) {
	if !strings.HasPrefix(content, "---") {
		return
	}

	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return
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
			name = val
		case "description":
			desc = val
		}
	}
	return
}

func (l *Loader) projectPath() string {
	return filepath.Join(l.workspace, ".sumi", "skills")
}

func (l *Loader) globalPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sumi", "skills")
}
