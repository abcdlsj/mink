package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoaderParsesSkillCards(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	root := filepath.Join(dir, ".sumi", "skills", "bark")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
name: bark-notify
description: Send controlled Bark notifications
when_to_use: notify user after monitoring alert
risk: notification
env: SUMI_NOTIFY_BARK_URL
entrypoints: bark_notify.py
examples: alert title, alert body
---

Use the script.
`
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cards := NewLoader(dir).Cards()
	if len(cards) != 1 {
		t.Fatalf("cards = %+v", cards)
	}
	for _, want := range []string{
		"- bark-notify: Send controlled Bark notifications",
		"when: notify user after monitoring alert",
		"risk: notification",
		"env: SUMI_NOTIFY_BARK_URL",
		"entrypoints: bark_notify.py",
		"examples: alert title | alert body",
	} {
		if !strings.Contains(cards[0], want) {
			t.Fatalf("card missing %q:\n%s", want, cards[0])
		}
	}
}
