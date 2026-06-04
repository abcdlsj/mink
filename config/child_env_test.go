package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChildEnvIncludesScopedEnv(t *testing.T) {
	cfg := Config{ScopedEnv: map[string]string{
		"SUMI_EMBY_SERVER": "https://emby.example",
		"bad=key":          "ignored",
	}}
	env := envMap(cfg.ChildEnv())

	if got := env["SUMI_EMBY_SERVER"]; got != "https://emby.example" {
		t.Fatalf("SUMI_EMBY_SERVER = %q", got)
	}
	if _, ok := env["bad=key"]; ok {
		t.Fatal("invalid env key was included")
	}
}

func TestLoadScopedEnvFlattensCapabilitySections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
active_model = "main"

[api_keys]
OPENAI_API_KEY = "secret"

[emby]
server = "https://emby.example"
username = "alice"

[notify]
bark_url = "https://api.day.app/key"

[notify.extra]
enabled = true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	env := loadScopedEnv(path)
	if got := env["SUMI_EMBY_SERVER"]; got != "https://emby.example" {
		t.Fatalf("SUMI_EMBY_SERVER = %q", got)
	}
	if got := env["SUMI_EMBY_USERNAME"]; got != "alice" {
		t.Fatalf("SUMI_EMBY_USERNAME = %q", got)
	}
	if got := env["SUMI_NOTIFY_BARK_URL"]; got != "https://api.day.app/key" {
		t.Fatalf("SUMI_NOTIFY_BARK_URL = %q", got)
	}
	if got := env["SUMI_NOTIFY_EXTRA_ENABLED"]; got != "true" {
		t.Fatalf("SUMI_NOTIFY_EXTRA_ENABLED = %q", got)
	}
	if _, ok := env["SUMI_API_KEYS_OPENAI_API_KEY"]; ok {
		t.Fatal("core api_keys section was injected")
	}
}

func TestEnvNameNormalizesParts(t *testing.T) {
	if got := envName([]string{"emby-server", "api.key"}); got != "SUMI_EMBY_SERVER_API_KEY" {
		t.Fatalf("envName = %q", got)
	}
}
