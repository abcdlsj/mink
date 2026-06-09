package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
)

func TestSkillDirectoryReportsConfigNeedsAndRecentUse(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "emby", `---
name: emby
description: Media search
when: Browse Emby media
risk: local script
env: [SUMI_EMBY_SERVER, SUMI_EMBY_USERNAME]
entrypoints: [bash]
examples: [search "akira"]
---

# Emby
`)
	a, err := New(config.Config{
		DataDir:   filepath.Join(dir, "data"),
		Workspace: dir,
		ScopedEnv: map[string]string{
			"SUMI_EMBY_SERVER": "https://emby.example",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	a.Bus().Publish(bus.Event{Type: bus.SkillUsed, Text: "emby"})

	skills := a.SkillDirectory()
	got, ok := findSkillDirectoryItem(skills, "emby")
	if !ok {
		t.Fatalf("emby skill missing: %#v", skills)
	}
	if got.Configured {
		t.Fatal("skill should be marked unconfigured when one env need is missing")
	}
	if len(got.EnvNeeds) != 2 || !got.EnvNeeds[0].Configured || got.EnvNeeds[1].Configured {
		t.Fatalf("env needs = %#v, want first configured and second missing", got.EnvNeeds)
	}
	if len(got.MissingEnv) != 1 || got.MissingEnv[0] != "SUMI_EMBY_USERNAME" {
		t.Fatalf("missing env = %#v", got.MissingEnv)
	}
	if got.EnvNeeds[1].Hint != "Set [emby].username in config.toml or export SUMI_EMBY_USERNAME" {
		t.Fatalf("hint = %q", got.EnvNeeds[1].Hint)
	}
	if got.LastAction != "used" || got.LastUsed == nil || got.LastUsed.IsZero() {
		t.Fatalf("recent skill state = action %q used %v", got.LastAction, got.LastUsed)
	}
	if got.Body != "" {
		t.Fatal("directory list must not include full skill body")
	}
}

func TestSkillDetailIncludesBody(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "bark-notify", `---
name: bark-notify
description: Notify via Bark
env: [notify.bark_url]
---

# Bark Notify
`)
	a, err := New(config.Config{
		DataDir:   filepath.Join(dir, "data"),
		Workspace: dir,
		ScopedEnv: map[string]string{
			"SUMI_NOTIFY_BARK_URL": "https://example.invalid/key",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	detail, ok := a.SkillDetail("BARK-NOTIFY")
	if !ok {
		t.Fatal("skill detail not found")
	}
	if !detail.Configured || len(detail.MissingEnv) != 0 {
		t.Fatalf("detail config = configured %v missing %#v", detail.Configured, detail.MissingEnv)
	}
	if detail.Body == "" || detail.Name != "bark-notify" {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestSkillCommandsShowReadinessAndDetail(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "notify", `---
name: notify
description: Send notifications
env: [SUMI_NOTIFY_BARK_URL]
examples: [send alert]
---

# Notify
`)
	a, err := New(config.Config{
		DataDir:   filepath.Join(dir, "data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	list, err := a.HandleInput(context.Background(), "cli", "/skills")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "notify [missing SUMI_NOTIFY_BARK_URL]") {
		t.Fatalf("/skills = %q", list)
	}

	detail, err := a.HandleInput(context.Background(), "cli", "/skill notify")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "Env: SUMI_NOTIFY_BARK_URL [missing]") || !strings.Contains(detail, "# Notify") {
		t.Fatalf("/skill notify = %q", detail)
	}
}

func writeTestSkill(t *testing.T, workspace, name, body string) {
	t.Helper()
	dir := filepath.Join(workspace, ".sumi", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findSkillDirectoryItem(skills []SkillDirectoryItem, name string) (SkillDirectoryItem, bool) {
	for _, skill := range skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return SkillDirectoryItem{}, false
}
