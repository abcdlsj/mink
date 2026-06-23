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

func TestExternalRuntimeEnvDoesNotInheritHostState(t *testing.T) {
	t.Setenv("HOME", "/host/home")
	t.Setenv("CODEX_HOME", "/host/codex")
	t.Setenv("CLAUDE_CONFIG_DIR", "/host/claude")
	t.Setenv("SUMI_HOST_ONLY", "ambient")
	t.Setenv("HTTP_PROXY", "http://proxy.example")
	t.Setenv("PATH", "/bin")

	cfg := Config{
		Provider: "anthropic",
		APIKey:   "anthropic-key",
		ScopedEnv: map[string]string{
			"SUMI_EMBY_SERVER":    "https://emby.example",
			"SUMI_CLAUDE_API_KEY": "scoped-claude-key",
			"SUMI_CODEX_API_KEY":  "scoped-codex-key",
			"bad=key":             "ignored",
		},
	}
	env := envMap(cfg.ExternalRuntimeEnv())

	if _, ok := env["HOME"]; ok {
		t.Fatal("external env inherited host HOME")
	}
	if _, ok := env["CODEX_HOME"]; ok {
		t.Fatal("external env inherited host CODEX_HOME")
	}
	if _, ok := env["CLAUDE_CONFIG_DIR"]; ok {
		t.Fatal("external env inherited host CLAUDE_CONFIG_DIR")
	}
	if _, ok := env["SUMI_HOST_ONLY"]; ok {
		t.Fatal("external env inherited ambient SUMI_*")
	}
	if got := env["PATH"]; got != "/bin" {
		t.Fatalf("PATH = %q", got)
	}
	if got := env["HTTP_PROXY"]; got != "http://proxy.example" {
		t.Fatalf("HTTP_PROXY = %q", got)
	}
	if got := env["SUMI_EMBY_SERVER"]; got != "https://emby.example" {
		t.Fatalf("SUMI_EMBY_SERVER = %q", got)
	}
	if got := env["ANTHROPIC_API_KEY"]; got != "scoped-claude-key" {
		t.Fatalf("ANTHROPIC_API_KEY = %q", got)
	}
	if got := env["OPENAI_API_KEY"]; got != "scoped-codex-key" {
		t.Fatalf("OPENAI_API_KEY = %q", got)
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
