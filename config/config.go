package config

import (
	"os"
	"os/exec"
	"path/filepath"
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
	SoulPath    string            `toml:"soul_path"`
	Prompt      string            `toml:"prompt"`
	MaxTokens   int               `toml:"max_tokens"`
	Headers     map[string]string `toml:"headers"`
	Reasoning   bool              `toml:"reasoning"`
	WebAddr     string            `toml:"web_addr"`
	Compact     CompactConfig     `toml:"compact"`
	Telegram    TelegramConfig    `toml:"telegram"`
	BraveSearch BraveConfig       `toml:"brave_search"`

	Active ModelConfig `toml:"-"`
}

type ModelConfig struct {
	Provider      string            `toml:"provider"`
	Model         string            `toml:"model"`
	APIKey        string            `toml:"api_key"`
	BaseURL       string            `toml:"base_url"`
	Headers       map[string]string `toml:"headers"`
	MaxTokens     int               `toml:"max_tokens"`
	ContextWindow int               `toml:"context_window"`
	Reasoning     bool              `toml:"reasoning"`
}

type CompactConfig struct {
	Auto               bool `toml:"auto"`
	TriggerTokens      int  `toml:"trigger_tokens"`
	TriggerMessages    int  `toml:"trigger_messages"`
	KeepRecentMessages int  `toml:"keep_recent_messages"`
	ReserveTokens      int  `toml:"reserve_tokens"`
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
	c := defaultConfig()
	if path := ConfigPath(); path != "" {
		_, _ = toml.DecodeFile(path, &c)
	}
	c.applyEnv()
	c.Normalize()
	return c
}

func (c *Config) Normalize() {
	c.normalizePaths()
	c.normalizeDefaults()
	c.normalizeCollections()
	c.normalizeTelegram()
	if !c.ResolveActive() {
		c.applyDetected()
		_ = c.ResolveActive()
	}
}

func (c *Config) normalizePaths() {
	if c.Runtime == "" {
		c.Runtime = DetectRuntime()
	}
	if c.Workspace == "" {
		c.Workspace, _ = os.Getwd()
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
}

func (c *Config) normalizeDefaults() {
	if c.MaxTokens == 0 {
		c.MaxTokens = 4096
	}
	if c.WebAddr == "" {
		c.WebAddr = "127.0.0.1:7788"
	}
	if c.Compact.KeepRecentMessages < 0 {
		c.Compact.KeepRecentMessages = 8
	}
}

func (c *Config) normalizeCollections() {
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	if c.Models == nil {
		c.Models = map[string]ModelConfig{}
	}
	if c.APIKeys == nil {
		c.APIKeys = map[string]string{}
	}
}

func (c *Config) normalizeTelegram() {
	c.Telegram.MentionMode = normalizeTelegramMode(c.Telegram.MentionMode)
	c.Telegram.SessionScope = normalizeTelegramScope(c.Telegram.SessionScope)
}

func (c *Config) Ready() bool {
	if c.Active.Provider != "" && c.Active.Model != "" && c.Active.APIKey != "" {
		return true
	}
	return strings.TrimSpace(c.Provider) != "" && strings.TrimSpace(c.Model) != "" && strings.TrimSpace(c.APIKey) != ""
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

func (c Config) PermissionsPath() string {
	return filepath.Join(c.DataRoot(), "state", "permissions.json")
}

func (c Config) ResolvedSoulPath() string {
	if path := strings.TrimSpace(c.SoulPath); path != "" {
		return path
	}
	return filepath.Join(c.DataRoot(), "SOUL.md")
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
		Provider:      blank(mc.Provider, c.Provider),
		Model:         blank(mc.Model, c.Model),
		APIKey:        c.expandKey(blank(mc.APIKey, c.APIKey)),
		BaseURL:       blank(mc.BaseURL, c.BaseURL),
		Headers:       headers,
		MaxTokens:     mc.MaxTokens,
		ContextWindow: mc.ContextWindow,
		Reasoning:     mc.Reasoning || c.Reasoning,
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
	applyEnv(&c.Provider, "MINK_PROVIDER")
	applyEnv(&c.Model, "MINK_MODEL")
	applyEnv(&c.APIKey, "MINK_API_KEY")
	applyEnv(&c.BaseURL, "MINK_BASE_URL")
	applyEnv(&c.Runtime, "MINK_RUNTIME")
	applyEnv(&c.DataDir, "MINK_DATA_DIR")
	applyEnv(&c.SoulPath, "MINK_SOUL_PATH")
	applyEnv(&c.WebAddr, "MINK_WEB_ADDR")
	applyEnv(&c.Telegram.Token, "TELEGRAM_TOKEN", "MINK_TELEGRAM_TOKEN")
	applyEnv(&c.Telegram.MentionMode, "MINK_TELEGRAM_MENTION_MODE")
	applyEnv(&c.Telegram.SessionScope, "MINK_TELEGRAM_SESSION_SCOPE")
	applyEnv(&c.BraveSearch.APIKey, "BRAVE_SEARCH_API_KEY", "MINK_BRAVE_SEARCH_API_KEY")
}

func applyEnv(dst *string, keys ...string) {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			*dst = v
			return
		}
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
