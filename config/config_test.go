package config

import "testing"

func TestConfigSoulPathUsesOverride(t *testing.T) {
	c := Config{DataDir: "/tmp/data", SoulPath: "/tmp/custom/SOUL.md"}
	if got := c.ResolvedSoulPath(); got != "/tmp/custom/SOUL.md" {
		t.Fatalf("ResolvedSoulPath() = %q", got)
	}
}

func TestConfigSoulPathDefaultsToDataDir(t *testing.T) {
	c := Config{DataDir: "/tmp/data"}
	if got := c.ResolvedSoulPath(); got != "/tmp/data/SOUL.md" {
		t.Fatalf("ResolvedSoulPath() = %q", got)
	}
}
