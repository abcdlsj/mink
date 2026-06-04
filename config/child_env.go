package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

func (c Config) ChildEnv() []string {
	env := envMap(os.Environ())
	for key, value := range c.ScopedEnv {
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			continue
		}
		env[key] = value
	}
	return flattenEnv(env)
}

func loadScopedEnv(path string) map[string]string {
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil
	}

	out := map[string]string{}
	for section, value := range raw {
		if !childEnvSection(section) {
			continue
		}
		flattenConfigEnv(out, []string{section}, value)
	}
	return out
}

func flattenConfigEnv(out map[string]string, path []string, value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			flattenConfigEnv(out, append(path, key), child)
		}
	case string:
		out[envName(path)] = v
	case bool, int64, int, float64:
		out[envName(path)] = fmt.Sprint(v)
	}
}

func childEnvSection(section string) bool {
	switch strings.ToLower(strings.TrimSpace(section)) {
	case "",
		"active_model",
		"default_model",
		"cheap_model",
		"models",
		"api_keys",
		"provider",
		"model",
		"api_key",
		"base_url",
		"runtime",
		"data_dir",
		"workspace",
		"soul_path",
		"prompt",
		"max_tokens",
		"headers",
		"reasoning",
		"web_addr",
		"compact",
		"telegram",
		"brave_search",
		"collab",
		"status_line",
		"default_persona":
		return false
	default:
		return true
	}
}

func envName(parts []string) string {
	clean := make([]string, 0, len(parts)+1)
	clean = append(clean, "SUMI")
	for _, part := range parts {
		name := normalizeEnvPart(part)
		if name != "" {
			clean = append(clean, name)
		}
	}
	return strings.Join(clean, "_")
}

func normalizeEnvPart(s string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func flattenEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func envMap(src []string) map[string]string {
	out := make(map[string]string, len(src))
	for _, entry := range src {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !validEnvKey(key) {
			continue
		}
		out[key] = value
	}
	return out
}

func validEnvKey(key string) bool {
	return key != "" && !strings.Contains(key, "=")
}
