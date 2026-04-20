package config

import "os"

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
