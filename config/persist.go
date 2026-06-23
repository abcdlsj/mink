package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func PersistModel(c Config) error {
	return PersistModelPath(ConfigPath(), c)
}

func PersistModelPath(path string, c Config) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	out := persistModelText(string(data), c)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), mode)
}

func persistModelText(s string, c Config) string {
	if strings.TrimSpace(c.ActiveModel) != "" {
		return setTopKeys(s, map[string]*string{
			"active_model": &c.ActiveModel,
		})
	}
	active := ""
	def := ""
	return setTopKeys(s, map[string]*string{
		"active_model":  &active,
		"default_model": &def,
		"provider":      &c.Provider,
		"model":         &c.Model,
	})
}

func setTopKeys(s string, values map[string]*string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	seen := map[string]bool{}
	firstSection := len(lines)
	inTop := true
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			if firstSection == len(lines) {
				firstSection = i
			}
			inTop = false
		}
		if !inTop {
			continue
		}
		key, ok := tomlKey(line)
		if !ok {
			continue
		}
		v, ok := values[key]
		if !ok {
			continue
		}
		lines[i] = key + " = " + strconv.Quote(*v)
		seen[key] = true
	}
	var add []string
	for _, key := range []string{"active_model", "default_model", "provider", "model"} {
		v, ok := values[key]
		if !ok || seen[key] {
			continue
		}
		add = append(add, key+" = "+strconv.Quote(*v))
	}
	if len(add) > 0 {
		lines = append(lines[:firstSection], append(add, lines[firstSection:]...)...)
	}
	return strings.Join(lines, "\n") + "\n"
}

func tomlKey(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
		return "", false
	}
	i := strings.Index(t, "=")
	if i < 0 {
		return "", false
	}
	key := strings.TrimSpace(t[:i])
	if key == "" || strings.ContainsAny(key, "\"'.") {
		return "", false
	}
	return key, true
}
