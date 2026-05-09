package collab

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (m *manager) loadTeams() {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var loaded map[string]map[string]string
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for src, aliases := range loaded {
		if m.teams[src] == nil {
			m.teams[src] = map[string]string{}
		}
		for a, rt := range aliases {
			m.teams[src][a] = rt
		}
	}
}

func (m *manager) saveTeams(snapshot map[string]map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), "teams-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), m.path)
}
