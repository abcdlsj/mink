package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	ActiveModel string                 `toml:"active_model"`
	Default     string                 `toml:"default_model"`
	Cheap       string                 `toml:"cheap_model"`
	Models      map[string]ModelConfig `toml:"models"`
	APIKeys     map[string]string      `toml:"api_keys"`

	Provider    string            `toml:"provider"`
	Model       string            `toml:"model"`
	APIKey      string            `toml:"api_key"`
	BaseURL     string            `toml:"base_url"`
	Runtime     string            `toml:"runtime"`
	DataDir     string            `toml:"data_dir"`
	Workspace   string            `toml:"workspace"`
	Prompt      string            `toml:"prompt"`
	MaxTokens   int               `toml:"max_tokens"`
	Headers     map[string]string `toml:"headers"`
	Reasoning   bool              `toml:"reasoning"`
	WebAddr     string            `toml:"web_addr"`
	Telegram    TelegramConfig    `toml:"telegram"`
	BraveSearch BraveConfig       `toml:"brave_search"`

	Active ModelConfig `toml:"-"`
}

type ModelConfig struct {
	Provider  string            `toml:"provider"`
	Model     string            `toml:"model"`
	APIKey    string            `toml:"api_key"`
	BaseURL   string            `toml:"base_url"`
	Headers   map[string]string `toml:"headers"`
	MaxTokens int               `toml:"max_tokens"`
	Reasoning bool              `toml:"reasoning"`
}

type TelegramConfig struct {
	Token        string `toml:"token"`
	MentionMode  string `toml:"mention_mode"`
	SessionScope string `toml:"session_scope"`
}

type BraveConfig struct {
	APIKey string `toml:"api_key"`
}

type Option struct {
	Provider string
	Model    string
	Source   string
	APIKey   string
	BaseURL  string
}

func Load() Config {
	c := Config{
		MaxTokens: 4096,
		Headers:   map[string]string{},
		WebAddr:   "127.0.0.1:7788",
		Models:    map[string]ModelConfig{},
		APIKeys:   map[string]string{},
	}
	if path := ConfigPath(); path != "" {
		_, _ = toml.DecodeFile(path, &c)
	}
	c.applyEnv()
	c.Normalize()
	return c
}

func (c *Config) Normalize() {
	if c.Runtime == "" {
		c.Runtime = DetectRuntime()
	}
	if c.Workspace == "" {
		c.Workspace, _ = os.Getwd()
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 4096
	}
	if c.WebAddr == "" {
		c.WebAddr = "127.0.0.1:7788"
	}
	switch strings.TrimSpace(strings.ToLower(c.Telegram.MentionMode)) {
	case "", "always":
		c.Telegram.MentionMode = "always"
	case "smart":
		c.Telegram.MentionMode = "smart"
	case "mention_only":
		c.Telegram.MentionMode = "mention_only"
	default:
		c.Telegram.MentionMode = "always"
	}
	switch strings.TrimSpace(strings.ToLower(c.Telegram.SessionScope)) {
	case "", "chat":
		c.Telegram.SessionScope = "chat"
	case "thread":
		c.Telegram.SessionScope = "thread"
	default:
		c.Telegram.SessionScope = "chat"
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	if c.Models == nil {
		c.Models = map[string]ModelConfig{}
	}
	if c.APIKeys == nil {
		c.APIKeys = map[string]string{}
	}
	if !c.ResolveActive() {
		c.applyDetected()
		_ = c.ResolveActive()
	}
}

func (c *Config) Ready() bool {
	if c.Active.Provider != "" && c.Active.Model != "" && c.Active.APIKey != "" {
		return true
	}
	return strings.TrimSpace(c.Provider) != "" && strings.TrimSpace(c.Model) != "" && strings.TrimSpace(c.APIKey) != ""
}

func (c *Config) Resolve(provider, model string) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	model = strings.TrimSpace(model)
	for name, mc := range c.Models {
		if strings.TrimSpace(strings.ToLower(mc.Provider)) != provider {
			continue
		}
		if model != "" && strings.TrimSpace(mc.Model) != model {
			continue
		}
		c.ActiveModel = name
		c.useModel(mc)
		if model != "" {
			c.Model = model
			c.Active.Model = model
		}
		return
	}
	for _, opt := range Detect() {
		if opt.Provider != provider {
			continue
		}
		c.ActiveModel = ""
		c.Provider = opt.Provider
		c.Model = blank(model, opt.Model)
		c.APIKey = opt.APIKey
		c.BaseURL = opt.BaseURL
		c.Active = ModelConfig{
			Provider:  c.Provider,
			Model:     c.Model,
			APIKey:    c.APIKey,
			BaseURL:   c.BaseURL,
			Headers:   cloneHeaders(c.Headers),
			MaxTokens: c.MaxTokens,
			Reasoning: c.Reasoning,
		}
		return
	}
	c.ActiveModel = ""
	c.Provider = provider
	if model != "" {
		c.Model = model
	}
	c.Active = ModelConfig{
		Provider:  c.Provider,
		Model:     c.Model,
		APIKey:    c.expandKey(c.APIKey),
		BaseURL:   c.BaseURL,
		Headers:   cloneHeaders(c.Headers),
		MaxTokens: c.MaxTokens,
		Reasoning: c.Reasoning,
	}
}

func (c *Config) ResolveActive() bool {
	if c.ActiveModel != "" {
		if mc, ok := c.Models[c.ActiveModel]; ok {
			c.useModel(mc)
			return true
		}
	}
	if c.Default != "" {
		if mc, ok := c.Models[c.Default]; ok {
			c.ActiveModel = c.Default
			c.useModel(mc)
			return true
		}
	}
	if c.Provider != "" && c.Model != "" {
		c.Active = ModelConfig{
			Provider:  c.Provider,
			Model:     c.Model,
			APIKey:    c.expandKey(c.APIKey),
			BaseURL:   c.BaseURL,
			Headers:   cloneHeaders(c.Headers),
			MaxTokens: max(c.MaxTokens, 4096),
			Reasoning: c.Reasoning,
		}
		c.syncActive()
		return true
	}
	if len(c.Models) == 0 {
		return false
	}
	names := make([]string, 0, len(c.Models))
	for name := range c.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	c.ActiveModel = names[0]
	if c.Default == "" {
		c.Default = c.ActiveModel
	}
	c.useModel(c.Models[c.ActiveModel])
	return true
}

func (c Config) DataRoot() string {
	if strings.TrimSpace(c.DataDir) != "" {
		return strings.TrimSpace(c.DataDir)
	}
	return DefaultDataDir()
}

func (c Config) MemoryDir() string {
	return filepath.Join(c.DataRoot(), "memory")
}

func (c Config) CronPath() string {
	return filepath.Join(c.DataRoot(), "cron", "tasks.json")
}

func ConfigPath() string {
	return filepath.Join(DefaultDataDir(), "config.toml")
}

func DefaultDataDir() string {
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

func (c *Config) useModel(mc ModelConfig) {
	headers := cloneHeaders(mc.Headers)
	if len(headers) == 0 {
		headers = cloneHeaders(c.Headers)
	}
	c.Active = ModelConfig{
		Provider:  blank(mc.Provider, c.Provider),
		Model:     blank(mc.Model, c.Model),
		APIKey:    c.expandKey(blank(mc.APIKey, c.APIKey)),
		BaseURL:   blank(mc.BaseURL, c.BaseURL),
		Headers:   headers,
		MaxTokens: mc.MaxTokens,
		Reasoning: mc.Reasoning || c.Reasoning,
	}
	if c.Active.MaxTokens == 0 {
		c.Active.MaxTokens = c.MaxTokens
	}
	if c.Active.MaxTokens == 0 {
		c.Active.MaxTokens = 4096
	}
	c.syncActive()
}

func (c *Config) syncActive() {
	c.Provider = c.Active.Provider
	c.Model = c.Active.Model
	c.APIKey = c.Active.APIKey
	c.BaseURL = c.Active.BaseURL
	c.Headers = cloneHeaders(c.Active.Headers)
	c.MaxTokens = c.Active.MaxTokens
	c.Reasoning = c.Active.Reasoning
	if c.Default == "" && c.ActiveModel != "" {
		c.Default = c.ActiveModel
	}
}

func (c *Config) expandKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "${") {
		return expand(v, c.APIKeys)
	}
	if key, ok := c.APIKeys[v]; ok && key != "" {
		return key
	}
	if key := os.Getenv(v); key != "" {
		return key
	}
	return v
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
	if v := os.Getenv("MINK_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("MINK_WEB_ADDR"); v != "" {
		c.WebAddr = v
	}
	if v := os.Getenv("TELEGRAM_TOKEN"); v != "" {
		c.Telegram.Token = v
	}
	if v := os.Getenv("MINK_TELEGRAM_TOKEN"); v != "" {
		c.Telegram.Token = v
	}
	if v := os.Getenv("MINK_TELEGRAM_MENTION_MODE"); v != "" {
		c.Telegram.MentionMode = v
	}
	if v := os.Getenv("MINK_TELEGRAM_SESSION_SCOPE"); v != "" {
		c.Telegram.SessionScope = v
	}
	if v := os.Getenv("BRAVE_SEARCH_API_KEY"); v != "" {
		c.BraveSearch.APIKey = v
	}
	if v := os.Getenv("MINK_BRAVE_SEARCH_API_KEY"); v != "" {
		c.BraveSearch.APIKey = v
	}
}

func cloneHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func expand(s string, keys map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+2 < len(s) && s[i] == '$' && s[i+1] == '{' {
			j := i + 2
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j < len(s) {
				key := s[i+2 : j]
				if v, ok := keys[key]; ok && v != "" {
					b.WriteString(v)
				} else if v := os.Getenv(key); v != "" {
					b.WriteString(v)
				} else {
					b.WriteString(s[i : j+1])
				}
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
