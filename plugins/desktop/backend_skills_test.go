package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillsDirectoryViewsExposeReadinessAndDetailBody(t *testing.T) {
	b, a := newBackendWithApp(t)
	writeDesktopTestSkill(t, a.Config().Workspace, "notify", `---
name: notify
description: Send notifications
env: [SUMI_NOTIFY_BARK_URL]
---

# Notify
`)

	list := b.ListSkills()
	got, ok := findDesktopSkillView(list, "notify")
	if !ok {
		t.Fatalf("notify skill missing: %#v", list)
	}
	if got.Configured {
		t.Fatal("skill should be unconfigured without SUMI_NOTIFY_BARK_URL")
	}
	if got.Body != "" {
		t.Fatal("skill list must not include body")
	}
	if len(got.EnvNeeds) != 1 || got.EnvNeeds[0].Hint == "" {
		t.Fatalf("env needs = %#v", got.EnvNeeds)
	}

	detail := b.GetSkill("notify")
	if detail.Name != "notify" || detail.Body == "" {
		t.Fatalf("detail = %#v", detail)
	}
}

func findDesktopSkillView(skills []SkillView, name string) (SkillView, bool) {
	for _, skill := range skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return SkillView{}, false
}

func writeDesktopTestSkill(t *testing.T, workspace, name, body string) {
	t.Helper()
	dir := filepath.Join(workspace, ".sumi", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
