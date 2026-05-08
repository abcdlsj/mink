package config

import (
	"os"
	"os/exec"
)

type Option struct {
	Provider string
	Model    string
	Source   string
	APIKey   string
	BaseURL  string
}

func Detect() []Option {
	var opts []Option
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		opts = append(opts, Option{
			Provider: "openai",
			Model:    envOr("SUMI_OPENAI_MODEL", envOr("SUMI_MODEL", "gpt-4.1-mini")),
			Source:   "OPENAI_API_KEY",
			APIKey:   key,
			BaseURL:  envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		})
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		opts = append(opts, Option{
			Provider: "anthropic",
			Model:    envOr("SUMI_ANTHROPIC_MODEL", envOr("SUMI_MODEL", "claude-sonnet-4-20250514")),
			Source:   "ANTHROPIC_API_KEY",
			APIKey:   key,
		})
	}
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		opts = append(opts, Option{
			Provider: "openrouter",
			Model:    envOr("SUMI_OPENROUTER_MODEL", envOr("SUMI_MODEL", "openai/gpt-4.1-mini")),
			Source:   "OPENROUTER_API_KEY",
			APIKey:   key,
			BaseURL:  envOr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		})
	}
	return opts
}

func DetectRuntime() string {
	for _, name := range []string{"claude", "codex"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return "native"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
