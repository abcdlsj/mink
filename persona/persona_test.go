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

	p, err := r.Create("Debug", Meta{Display: "Debug", Runtime: "claude", Description: "bug hunter"}, "# Debug\nkeep calm")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID != "debug" {
		t.Fatalf("id = %q, want debug", p.ID)
	}
	if p.Runtime != "claude" {
		t.Fatalf("runtime = %q", p.Runtime)
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
	if _, err := os.Stat(filepath.Join(dir, "x", "memory")); err != nil {
		t.Fatalf("memory dir missing: %v", err)
	}
}
