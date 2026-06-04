package config

import (
	"os"
	"sort"
	"strings"
)

func (c Config) ChildEnv() []string {
	env := envMap(os.Environ())

	if v := strings.TrimSpace(c.Notify.BarkURL); v != "" {
		env["SUMI_BARK_URL"] = v
	}
	for key, value := range c.Skills.Env {
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			continue
		}
		env[key] = value
	}

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
