package persona

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryLoadAndCreate(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	if err := r.Load(); err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}

	show := false
	p, err := r.Create("Debug", Meta{
		Display:       "Debug",
		Runtime:       "claude",
		Model:         "sonnet",
		Description:   "bug hunter",
		Capabilities:  []string{"assign", "task.execute", "task_assign", "TASK.REVIEW", "task.execute"},
		TaskPolicy:    "auto-commit",
		MemoryPolicy:  "auto-commit",
		ShowInSidebar: &show,
	}, "# Debug\nkeep calm")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID != "debug" {
		t.Fatalf("id = %q, want debug", p.ID)
	}
	if p.Runtime != "claude" {
		t.Fatalf("runtime = %q", p.Runtime)
	}
	if p.Model != "sonnet" {
		t.Fatalf("model = %q", p.Model)
	}
	if p.ShowInSidebar {
		t.Fatal("show_in_sidebar should be false")
	}
	if p.TaskPolicy != "auto_commit" {
		t.Fatalf("task_policy = %q, want auto_commit", p.TaskPolicy)
	}
	if p.MemoryPolicy != "auto_commit" {
		t.Fatalf("memory_policy = %q, want auto_commit", p.MemoryPolicy)
	}
	if got, want := p.Capabilities, []string{"task.assign", "task.execute", "task.review"}; len(got) != len(want) {
		t.Fatalf("capabilities = %#v, want %#v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("capabilities = %#v, want %#v", got, want)
			}
		}
	}
	if p.SoulPath == "" {
		t.Fatal("soul path should be set")
	}
	body, err := os.ReadFile(p.SoulPath)
	if err != nil {
		t.Fatalf("read soul: %v", err)
	}
	if string(body) != "# Debug\nkeep calm" {
		t.Fatalf("soul content = %q", body)
	}

	r2 := NewRegistry(dir)
	if err := r2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := r2.Get("debug")
	if got == nil {
		t.Fatal("persona lost after reload")
	}
	if got.Description != "bug hunter" {
		t.Fatalf("description = %q", got.Description)
	}
	if got.Model != "sonnet" || got.ShowInSidebar || got.TaskPolicy != "auto_commit" || got.MemoryPolicy != "auto_commit" || !got.HasCapability("execute") || !got.HasCapability("task.review") {
		t.Fatalf("meta not preserved: model=%q show=%v task_policy=%q memory_policy=%q caps=%#v", got.Model, got.ShowInSidebar, got.TaskPolicy, got.MemoryPolicy, got.Capabilities)
	}
}

func TestSanitizeID(t *testing.T) {
	cases := map[string]string{
		"Debug":          "debug",
		"Code Reviewer":  "codereviewer",
		"  sec/ops  ":    "secops",
		"é":              "",
		"foo-bar_baz123": "foo-bar_baz123",
	}
	for in, want := range cases {
		if got := sanitizeID(in); got != want {
			t.Fatalf("sanitizeID(%q) = %q, want %q", in, want, got)
		}
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	if _, err := r.Create("x", Meta{}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create("x", Meta{}, ""); err == nil {
		t.Fatal("expected duplicate error")
	}
	if _, err := os.Stat(filepath.Join(dir, "x", "memory")); !os.IsNotExist(err) {
		t.Fatalf("persona-local memory dir should not be created, err=%v", err)
	}
}
