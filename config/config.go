package config

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider  string            `toml:"provider"`
	Model     string            `toml:"model"`
	APIKey    string            `toml:"api_key"`
	BaseURL   string            `toml:"base_url"`
	Runtime   string            `toml:"runtime"`
	DBPath    string            `toml:"db_path"`
	Workspace string            `toml:"workspace"`
	Prompt    string            `toml:"prompt"`
	MaxTokens int               `toml:"max_tokens"`
	Headers   map[string]string `toml:"headers"`
}

type Option struct {
	Provider string
	Model    string
	Source   string
	APIKey   string
	BaseURL  string
}

func Load() Config {
	cfg := Config{
		Runtime:   "native",
		MaxTokens: 4096,
		Headers:   map[string]string{},
	}
	if path := ConfigPath(); path != "" {
		_, _ = toml.DecodeFile(path, &cfg)
	}
	cfg.applyEnv()
	cfg.Normalize()
	return cfg
}

func (c *Config) Normalize() {
	if c.Runtime == "" {
		c.Runtime = "native"
	}
	if c.Workspace == "" {
		c.Workspace, _ = os.Getwd()
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(DataDir(), "mink.db")
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 4096
	}
	if c.Provider == "" || c.Model == "" || c.APIKey == "" {
		c.applyDetected()
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
}

func (c *Config) Ready() bool {
	return strings.TrimSpace(c.Provider) != "" && strings.TrimSpace(c.Model) != "" && strings.TrimSpace(c.APIKey) != ""
}

func (c *Config) Resolve(provider, model string) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	model = strings.TrimSpace(model)
	for _, opt := range Detect() {
		if opt.Provider != provider {
			continue
		}
		c.Provider = opt.Provider
		c.APIKey = opt.APIKey
		c.BaseURL = opt.BaseURL
		if model != "" {
			c.Model = model
		} else {
			c.Model = opt.Model
		}
		return
	}
	c.Provider = provider
	if model != "" {
		c.Model = model
	}
}

func ConfigPath() string {
	return filepath.Join(DataDir(), "config.toml")
}

func DataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".mink"
	}
	return filepath.Join(home, ".mink")
}

func Detect() []Option {
	var opts []Option
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		opts = append(opts, Option{
			Provider: "openai",
			Model:    envOr("MINK_OPENAI_MODEL", envOr("MINK_MODEL", "gpt-4.1-mini")),
			Source:   "OPENAI_API_KEY",
			APIKey:   key,
			BaseURL:  envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		})
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		opts = append(opts, Option{
			Provider: "anthropic",
			Model:    envOr("MINK_ANTHROPIC_MODEL", envOr("MINK_MODEL", "claude-sonnet-4-20250514")),
			Source:   "ANTHROPIC_API_KEY",
			APIKey:   key,
		})
	}
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		opts = append(opts, Option{
			Provider: "openrouter",
			Model:    envOr("MINK_OPENROUTER_MODEL", envOr("MINK_MODEL", "openai/gpt-4.1-mini")),
			Source:   "OPENROUTER_API_KEY",
			APIKey:   key,
			BaseURL:  envOr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		})
	}
	if host, ok := ollamaURL(); ok {
		opts = append(opts, Option{
			Provider: "openai",
			Model:    envOr("MINK_OLLAMA_MODEL", envOr("MINK_MODEL", "qwen2.5-coder:latest")),
			Source:   "ollama",
			APIKey:   "ollama",
			BaseURL:  host + "/v1",
		})
	}
	return opts
}

func (c *Config) applyDetected() {
	opts := Detect()
	if len(opts) == 0 {
		return
	}
	if c.Provider == "" {
		c.Provider = opts[0].Provider
	}
	if c.Model == "" {
		c.Model = opts[0].Model
	}
	if c.APIKey == "" {
		c.APIKey = opts[0].APIKey
	}
	if c.BaseURL == "" {
		c.BaseURL = opts[0].BaseURL
	}
}

func (c *Config) applyEnv() {
	if v := os.Getenv("MINK_PROVIDER"); v != "" {
		c.Provider = v
	}
	if v := os.Getenv("MINK_MODEL"); v != "" {
		c.Model = v
	}
	if v := os.Getenv("MINK_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := os.Getenv("MINK_BASE_URL"); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv("MINK_RUNTIME"); v != "" {
		c.Runtime = v
	}
	if v := os.Getenv("MINK_DB_PATH"); v != "" {
		c.DBPath = v
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ollamaURL() (string, bool) {
	host := envOr("OLLAMA_HOST", "http://127.0.0.1:11434")
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(host + "/api/tags")
	if err != nil {
		return "", false
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", false
	}
	return strings.TrimRight(host, "/"), true
}
