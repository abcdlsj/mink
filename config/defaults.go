package config

import "strings"

func defaultConfig() Config {
	return Config{
		MaxTokens: 4096,
		Headers:   map[string]string{},
		WebAddr:   "127.0.0.1:7788",
		Models:    map[string]ModelConfig{},
		APIKeys:   map[string]string{},
		Compact: CompactConfig{
			TriggerTokens:      20000,
			TriggerMessages:    80,
			KeepRecentMessages: 8,
			ReserveTokens:      2048,
		},
	}
}

func normalizeTelegramMode(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "always":
		return "always"
	case "smart":
		return "smart"
	case "mention_only":
		return "mention_only"
	default:
		return "always"
	}
}

func normalizeTelegramScope(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "chat":
		return "chat"
	case "thread":
		return "thread"
	default:
		return "chat"
	}
}
