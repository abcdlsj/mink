package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestChildEnvMapsNotifyBarkURL(t *testing.T) {
	t.Setenv("SUMI_BARK_URL", "from-env")

	cfg := Config{Notify: NotifyConfig{BarkURL: "from-config"}}
	env := envMap(cfg.ChildEnv())

	if got := env["SUMI_BARK_URL"]; got != "from-config" {
		t.Fatalf("SUMI_BARK_URL = %q", got)
	}
}

func TestChildEnvIncludesSkillsEnv(t *testing.T) {
	cfg := Config{Skills: SkillsConfig{Env: map[string]string{
		"EMBY_SERVER":   "https://emby.example",
		"EMBY_USERNAME": "alice",
		"bad=key":       "ignored",
	}}}
	env := envMap(cfg.ChildEnv())

	if got := env["EMBY_SERVER"]; got != "https://emby.example" {
		t.Fatalf("EMBY_SERVER = %q", got)
	}
	if got := env["EMBY_USERNAME"]; got != "alice" {
		t.Fatalf("EMBY_USERNAME = %q", got)
	}
	if _, ok := env["bad=key"]; ok {
		t.Fatal("invalid env key was included")
	}
}

func TestSkillsEnvOverridesNotifyAlias(t *testing.T) {
	cfg := Config{
		Notify: NotifyConfig{BarkURL: "from-notify"},
		Skills: SkillsConfig{Env: map[string]string{
			"SUMI_BARK_URL": "from-skills",
		}},
	}
	env := envMap(cfg.ChildEnv())

	if got := env["SUMI_BARK_URL"]; got != "from-skills" {
		t.Fatalf("SUMI_BARK_URL = %q", got)
	}
}

func TestSkillsEnvDecodesFromTOML(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
[skills.env]
EMBY_SERVER = "https://emby.example"
EMBY_USERNAME = "alice"
`, &cfg); err != nil {
		t.Fatal(err)
	}

	if got := cfg.Skills.Env["EMBY_SERVER"]; got != "https://emby.example" {
		t.Fatalf("EMBY_SERVER = %q", got)
	}
	if got := cfg.Skills.Env["EMBY_USERNAME"]; got != "alice" {
		t.Fatalf("EMBY_USERNAME = %q", got)
	}
}
