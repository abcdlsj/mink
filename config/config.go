package config

import (
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	ActiveModel string                 `toml:"active_model"`
	Default     string                 `toml:"default_model"`
	Cheap       string                 `toml:"cheap_model"`
	Vision      string                 `toml:"vision_model"`
	Models      map[string]ModelConfig `toml:"models"`
	APIKeys     map[string]string      `toml:"api_keys"`

	Provider       string            `toml:"provider"`
	Model          string            `toml:"model"`
	APIKey         string            `toml:"api_key"`
	BaseURL        string            `toml:"base_url"`
	Runtime        string            `toml:"runtime"`
	DataDir        string            `toml:"data_dir"`
	Workspace      string            `toml:"workspace"`
	SoulPath       string            `toml:"soul_path"`
	Prompt         string            `toml:"prompt"`
	MaxTokens      int               `toml:"max_tokens"`
	Headers        map[string]string `toml:"headers"`
	Reasoning      bool              `toml:"reasoning"`
	WebAddr        string            `toml:"web_addr"`
	Compact        CompactConfig     `toml:"compact"`
	Telegram       TelegramConfig    `toml:"telegram"`
	BraveSearch    BraveConfig       `toml:"brave_search"`
	Collab         CollabConfig      `toml:"collab"`
	StatusLine     string            `toml:"status_line"`
	DefaultPersona string            `toml:"default_persona"`

	// ExternalInputBudgets declares the caller-confirmed usable INPUT token ceiling
	// for each external driver runtime (keys: "claude", "codex"). This is the only
	// thing that makes the hard-overflow guard enforceable for an external driver:
	// its real context window is owned by the driver, not derivable from the active
	// (summarizer) model, so we do not guess it. When a runtime has a positive
	// budget here the hard guard enforces it (compact-or-fail-closed); when it is
	// absent the guard stands down (unguarded) and the external driver owns and
	// reports its own overflow. This value is the input ceiling directly — it is NOT
	// a context window from which native MaxTokens/ReserveTokens are subtracted.
	ExternalInputBudgets map[string]int `toml:"external_input_budgets"`

	Active    ModelConfig       `toml:"-"`
	ScopedEnv map[string]string `toml:"-"`
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
	Token          string  `toml:"token"`
	MentionMode    string  `toml:"mention_mode"`
	SessionScope   string  `toml:"session_scope"`
	AllowedUserIDs []int64 `toml:"allowed_user_ids"`
	AllowedChatIDs []int64 `toml:"allowed_chat_ids"`
}

type BraveConfig struct {
	APIKey string `toml:"api_key"`
}

type CollabConfig struct {
	MaxConcurrent int `toml:"max_concurrent"`
	QueueDepth    int `toml:"queue_depth"`
	PollTimeoutMS int `toml:"poll_timeout_ms"`
}

func Load() Config {
	c := defaultConfig()
	if path := ConfigPath(); path != "" {
		_, _ = toml.DecodeFile(path, &c)
		c.ScopedEnv = loadScopedEnv(path)
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
	if c.Collab.MaxConcurrent <= 0 {
		c.Collab.MaxConcurrent = 4
	}
	if c.Collab.QueueDepth <= 0 {
		c.Collab.QueueDepth = 32
	}
	if c.Collab.QueueDepth < c.Collab.MaxConcurrent {
		c.Collab.QueueDepth = c.Collab.MaxConcurrent
	}
	if c.Collab.PollTimeoutMS <= 0 {
		c.Collab.PollTimeoutMS = 120000
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
	if c.ScopedEnv == nil {
		c.ScopedEnv = map[string]string{}
	}
	if c.ExternalInputBudgets == nil {
		c.ExternalInputBudgets = map[string]int{}
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
