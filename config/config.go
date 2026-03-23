package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Mode                 string                 `toml:"mode"`
	CustomPrompt         string                 `toml:"custom_prompt"`
	Stream               bool                   `toml:"stream"`
	TelegramStream       bool                   `toml:"telegram_stream"`
	TelegramMentionMode  string                 `toml:"telegram_mention_mode"`
	TelegramSessionScope string                 `toml:"telegram_session_scope"`
	MaxSteps             int                    `toml:"max_steps"`
	Timeout              TimeoutConfig          `toml:"timeout"`
	Compact              CompactConfig          `toml:"compact"`
	ActiveModel          string                 `toml:"active_model"`
	DefaultModel         string                 `toml:"default"`
	CheapModel           string                 `toml:"cheap"`
	Models               map[string]ModelConfig `toml:"models"`
	APIKeys              map[string]string      `toml:"api_keys"`
	Provider             string                 `toml:"provider"`
	Model                string                 `toml:"model"`
	APIKey               string                 `toml:"api_key"`
	BaseURL              string                 `toml:"base_url"`
	Headers              map[string]string      `toml:"headers"`
	Reasoning            bool                   `toml:"reasoning"`

	Active ModelConfig
}

type ModelConfig struct {
	Provider  string            `toml:"provider"`
	Model     string            `toml:"model"`
	APIKey    string            `toml:"api_key"`
	BaseURL   string            `toml:"base_url"`
	Headers   map[string]string `toml:"headers"`
	Reasoning bool              `toml:"reasoning"`
}

type CompactConfig struct {
	Auto               bool `toml:"auto"`
	TriggerTokens      int  `toml:"trigger_tokens"`
	TriggerMessages    int  `toml:"trigger_messages"`
	KeepRecentMessages int  `toml:"keep_recent_messages"`
}

type TimeoutConfig struct {
	Tool       int `toml:"tool"`
	Agent      int `toml:"agent"`
	Background int `toml:"background"`
	LLM        int `toml:"llm"`
}

func Load() Config {
	return LoadWithDir("mink")
}

func LoadWithDir(name string) Config {
	c := Config{Stream: true}

	configDir := defaultConfigDir(name)
	path := filepath.Join(configDir, "config.toml")

	if _, err := os.Stat(path); err == nil {
		_, _ = toml.DecodeFile(path, &c)
	}

	c.Normalize()
	c.ResolveActive()

	return c
}

func (c *Config) Normalize() {
	if c.Mode == "" {
		c.Mode = "tui"
	}
	if c.MaxSteps == 0 {
		c.MaxSteps = 100
	}
	if c.Timeout.Tool == 0 {
		c.Timeout.Tool = 60
	}
	if c.Timeout.Agent == 0 {
		c.Timeout.Agent = 600
	}
	if c.Timeout.Background == 0 {
		c.Timeout.Background = 1800
	}
	if c.Timeout.LLM == 0 {
		c.Timeout.LLM = 120
	}
	if c.Compact.TriggerTokens == 0 {
		c.Compact.TriggerTokens = 12000
	}
	if c.Compact.TriggerMessages == 0 {
		c.Compact.TriggerMessages = 80
	}
	if c.Compact.KeepRecentMessages == 0 {
		c.Compact.KeepRecentMessages = 20
	}
	switch strings.ToLower(strings.TrimSpace(c.TelegramMentionMode)) {
	case "", "always":
		c.TelegramMentionMode = "always"
	case "smart":
		c.TelegramMentionMode = "smart"
	case "mention_only":
		c.TelegramMentionMode = "mention_only"
	default:
		c.TelegramMentionMode = "always"
	}
	switch strings.ToLower(strings.TrimSpace(c.TelegramSessionScope)) {
	case "", "chat":
		c.TelegramSessionScope = "chat"
	case "thread":
		c.TelegramSessionScope = "thread"
	default:
		c.TelegramSessionScope = "chat"
	}
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
	c.Active = mc
	c.Active.APIKey = expand(mc.APIKey, c.APIKeys)
	return true
}

func (c *Config) ResolveDefaultModel() bool {
	if c.DefaultModel == "" {
		c.DefaultModel = c.ActiveModel
	}
	return ResolveModel(c, c.DefaultModel)
}

func (c *Config) ResolveCheapModel() bool {
	if c.CheapModel == "" {
		return false
	}
	return ResolveModel(c, c.CheapModel)
}

func (c *Config) ResolveActive() bool {
	if ResolveModel(c, c.ActiveModel) {
		return true
	}
	if c.DefaultModel != "" && ResolveModel(c, c.DefaultModel) {
		return true
	}

	if len(c.Models) > 0 {
		names := make([]string, 0, len(c.Models))
		for name := range c.Models {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			if c.DefaultModel == "" {
				c.DefaultModel = names[0]
			}
			return ResolveModel(c, names[0])
		}
	}

	if c.Provider == "" || c.Model == "" {
		return false
	}
	c.Active = ModelConfig{
		Provider:  c.Provider,
		Model:     c.Model,
		APIKey:    expand(c.APIKey, c.APIKeys),
		BaseURL:   c.BaseURL,
		Headers:   c.Headers,
		Reasoning: c.Reasoning,
	}
	if c.ActiveModel == "" {
		c.ActiveModel = "inline"
	}
	if c.DefaultModel == "" {
		c.DefaultModel = c.ActiveModel
	}
	return true
}

func (c *Config) Key(name string) string {
	if v, ok := c.APIKeys[name]; ok {
		return v
	}
	return os.Getenv(name)
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

func CronPath() string {
	return filepath.Join(DataDir("mink"), "cron.json")
}

func defaultConfigDir(name string) string {
	return DataDir(name)
}
