package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider     string            `toml:"provider"`
	BaseURL      string            `toml:"base_url"`
	APIKey       string            `toml:"api_key"`
	Model        string            `toml:"model"`
	Headers      map[string]string `toml:"headers"`
	Telegram     string            `toml:"telegram_token"`
	Mode         string            `toml:"mode"`
	CustomPrompt string            `toml:"custom_prompt"`
}

func Load() Config {
	c := Config{
		Provider: "openai",
		Model:    "gpt-4o",
		Mode:     "tui",
		Headers:  make(map[string]string),
	}

	home := os.ExpandEnv("$HOME/.mink")
	path := filepath.Join(home, "config.toml")

	if _, err := os.Stat(path); err == nil {
		toml.DecodeFile(path, &c)
	}

	if v := os.Getenv("OPENAI_API_KEY"); v != "" && c.APIKey == "" {
		c.APIKey = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" && c.APIKey == "" {
		c.APIKey = v
	}

	return c
}
