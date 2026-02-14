package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider     string                  `toml:"provider"`
	BaseURL      string                  `toml:"base_url"`
	APIKey       string                  `toml:"api_key"`
	BraveAPIKey  string                  `toml:"brave_api_key"`
	Model        string                  `toml:"model"`
	Headers      map[string]string       `toml:"headers"`
	Telegram     string                  `toml:"telegram_token"`
	Mode         string                  `toml:"mode"`
	CustomPrompt string                  `toml:"custom_prompt"`
	Stream       bool                    `toml:"stream"`
	MaxSteps     int                     `toml:"max_steps"`
	Timeout      TimeoutConfig           `toml:"timeout"`
	Compact      CompactConfig           `toml:"compact"`
	ActiveModel  string                  `toml:"active_model"`
	Models       map[string]ModelConfig  `toml:"models"`
	APIKeys      map[string]string       `toml:"api_keys"`
}

type ModelConfig struct {
	Provider string            `toml:"provider"`
	Model    string            `toml:"model"`
	APIKey   string            `toml:"api_key"`
	BaseURL  string            `toml:"base_url"`
	Headers  map[string]string `toml:"headers"`
}

type CompactConfig struct {
	Auto               bool `toml:"auto"`
	TriggerTokens      int  `toml:"trigger_tokens"`
	TriggerMessages    int  `toml:"trigger_messages"`
	KeepRecentMessages int  `toml:"keep_recent_messages"`
}

type TimeoutConfig struct {
	Tool       int `toml:"tool"`       // 单个工具执行超时，默认 60s
	Agent      int `toml:"agent"`      // Agent 整体运行超时，默认 600s
	Background int `toml:"background"` // 后台任务超时，默认 1800s
	LLM        int `toml:"llm"`        // LLM 请求超时，默认 120s
}

func Load() Config {
	return LoadWithDir("mink")
}

func LoadWithDir(name string) Config {
	c := Config{
		Provider: "openai",
		Model:    "gpt-4o",
		Mode:     "tui",
		Headers:  make(map[string]string),
		Stream:   true,
		MaxSteps: 100,
		Timeout: TimeoutConfig{
			Tool:       60,
			Agent:      600,
			Background: 1800,
			LLM:        120,
		},
		Compact: CompactConfig{
			Auto:               true,
			TriggerTokens:      12000,
			TriggerMessages:    80,
			KeepRecentMessages: 20,
		},
	}

	configDir := defaultConfigDir(name)
	path := filepath.Join(configDir, "config.toml")

	if _, err := os.Stat(path); err == nil {
		_, _ = toml.DecodeFile(path, &c)
	}

	if v := os.Getenv("OPENAI_API_KEY"); v != "" && c.APIKey == "" {
		c.APIKey = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" && c.APIKey == "" {
		c.APIKey = v
	}
	if v := os.Getenv("BRAVE_API_KEY"); v != "" && c.BraveAPIKey == "" {
		c.BraveAPIKey = v
	}

	ResolveModel(&c, c.ActiveModel)

	return c
}

func ResolveModel(c *Config, name string) bool {
	if name == "" || len(c.Models) == 0 {
		return false
	}
	mc, ok := c.Models[name]
	if !ok {
		return false
	}
	c.ActiveModel = name
	c.Provider = mc.Provider
	c.Model = mc.Model
	if mc.APIKey != "" {
		c.APIKey = expand(mc.APIKey, c.APIKeys)
	}
	if mc.BaseURL != "" {
		c.BaseURL = mc.BaseURL
	}
	if mc.Headers != nil {
		c.Headers = mc.Headers
	}
	return true
}

var envRe = regexp.MustCompile(`\$\{(\w+)\}`)

func expand(s string, keys map[string]string) string {
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		key := envRe.FindStringSubmatch(m)[1]
		if v, ok := keys[key]; ok && v != "" {
			return v
		}
		if v := os.Getenv(key); v != "" {
			return v
		}
		return m
	})
}

var activeModelRe = regexp.MustCompile(`(?m)^active_model\s*=\s*"[^"]*"`)

func SaveActiveModel(name string) error {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return os.WriteFile(path, []byte(fmt.Sprintf("active_model = %q\n", name)), 0644)
	}

	text := string(data)
	if activeModelRe.MatchString(text) {
		text = activeModelRe.ReplaceAllString(text, fmt.Sprintf("active_model = %q", name))
	} else {
		text = fmt.Sprintf("active_model = %q\n", name) + text
	}
	return os.WriteFile(path, []byte(text), 0644)
}

func DataDir(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "mink"
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		if strings.HasPrefix(name, ".") {
			return name
		}
		return "." + name
	}
	if strings.HasPrefix(name, ".") {
		return filepath.Join(home, name)
	}
	return filepath.Join(home, "."+name)
}

func ConfigPath() string {
	return filepath.Join(DataDir("mink"), "config.toml")
}

func SoulPath() string {
	return filepath.Join(DataDir("mink"), "SOUL.md")
}

func defaultConfigDir(name string) string {
	return DataDir(name)
}
