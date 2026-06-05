package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/tool"
)

func TestSkillToolsAuditListAndDescribe(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	root := filepath.Join(dir, ".sumi", "skills", "emby")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: emby\ndescription: Check Emby\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	var events []string
	reg := tool.NewRegistry(dir)
	RegisterTools(reg, NewLoader(dir), func(action, name string) {
		events = append(events, action+":"+name)
	})

	if out, err := reg.Run(context.Background(), "skills_list", nil); err != nil || out == "" {
		t.Fatalf("list = %q %v", out, err)
	}
	args, _ := json.Marshal(map[string]string{"name": "emby"})
	if out, err := reg.Run(context.Background(), "skills_describe", args); err != nil || out == "" {
		t.Fatalf("describe = %q %v", out, err)
	}

	want := []string{"listed:emby", "described:emby", "used:emby"}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %+v", events)
		}
	}
}
