package config

import "os"

func (c *Config) applyEnv() {
	applyEnv(&c.Provider, "SUMI_PROVIDER")
	applyEnv(&c.Model, "SUMI_MODEL")
	applyEnv(&c.APIKey, "SUMI_API_KEY")
	applyEnv(&c.BaseURL, "SUMI_BASE_URL")
	applyEnv(&c.Runtime, "SUMI_RUNTIME")
	applyEnv(&c.DataDir, "SUMI_DATA_DIR")
	applyEnv(&c.SoulPath, "SUMI_SOUL_PATH")
	applyEnv(&c.WebAddr, "SUMI_WEB_ADDR")
	applyEnv(&c.Telegram.Token, "TELEGRAM_TOKEN", "SUMI_TELEGRAM_TOKEN")
	applyEnv(&c.Telegram.MentionMode, "SUMI_TELEGRAM_MENTION_MODE")
	applyEnv(&c.Telegram.SessionScope, "SUMI_TELEGRAM_SESSION_SCOPE")
	applyEnv(&c.Notify.BarkURL, "SUMI_BARK_URL", "BARK_URL")
	applyEnv(&c.BraveSearch.APIKey, "BRAVE_SEARCH_API_KEY", "SUMI_BRAVE_SEARCH_API_KEY")
}

func applyEnv(dst *string, keys ...string) {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			*dst = v
			return
		}
	}
}
