package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/session"
)

func TestSystemPromptLayersRootAndPersonaSoul(t *testing.T) {
	dir := t.TempDir()
	globalSoul := filepath.Join(dir, "SOUL.md")
	personaSoul := filepath.Join(dir, "persona-SOUL.md")
	if err := os.WriteFile(globalSoul, []byte("GLOBAL SOUL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(personaSoul, []byte("PERSONA SOUL"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &RuntimeEnv{
		SoulPath: globalSoul,
		Persona: &Persona{
			ID:          "debug",
			Display:     "Debug",
			Description: "bug hunter",
			SoulPath:    personaSoul,
		},
	}
	s := BuildSystemPrompt(env, &Turn{Session: &session.Session{}})
	if !strings.Contains(s, "PERSONA SOUL") {
		t.Fatalf("prompt missing persona SOUL:\n%s", s)
	}
	if !strings.Contains(s, "GLOBAL SOUL") {
		t.Fatalf("prompt missing root SOUL:\n%s", s)
	}
	if !strings.Contains(s, "Sumi base identity (inherited root SOUL.md):") {
		t.Fatalf("prompt missing root SOUL section:\n%s", s)
	}
	if !strings.Contains(s, "Persona soul overlay (persona SOUL.md):") {
		t.Fatalf("prompt missing persona SOUL section:\n%s", s)
	}
	if !strings.Contains(s, "Persona: Debug (id=debug)") {
		t.Fatalf("prompt missing persona header:\n%s", s)
	}
	if !strings.Contains(s, "Role: bug hunter") {
		t.Fatalf("prompt missing role line:\n%s", s)
	}
	if !strings.Contains(s, "Persona runtime context:") || !strings.Contains(s, "memory_scopes: persona:debug") {
		t.Fatalf("prompt missing persona runtime context:\n%s", s)
	}
	if !strings.Contains(s, "explicit invocation") {
		t.Fatalf("prompt missing invocation guidance:\n%s", s)
	}
}

func TestSystemPromptFallsBackToGlobalSoulWhenPersonaHasNone(t *testing.T) {
	dir := t.TempDir()
	globalSoul := filepath.Join(dir, "SOUL.md")
	if err := os.WriteFile(globalSoul, []byte("GLOBAL SOUL"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &RuntimeEnv{
		SoulPath: globalSoul,
		Persona: &Persona{
			ID:      "reviewer",
			Display: "Reviewer",
		},
	}
	s := BuildSystemPrompt(env, &Turn{Session: &session.Session{}})
	if !strings.Contains(s, "GLOBAL SOUL") {
		t.Fatalf("expected fallback to global SOUL:\n%s", s)
	}
	if !strings.Contains(s, "Sumi base identity (inherited root SOUL.md):") {
		t.Fatalf("prompt missing root SOUL section:\n%s", s)
	}
	if strings.Contains(s, "Persona soul overlay") {
		t.Fatalf("unexpected persona SOUL section:\n%s", s)
	}
}

func TestSystemPromptFiltersRootPrivateSoulForPersona(t *testing.T) {
	dir := t.TempDir()
	globalSoul := filepath.Join(dir, "SOUL.md")
	personaSoul := filepath.Join(dir, "persona-SOUL.md")
	root := strings.Join([]string{
		"# Identity",
		"GLOBAL IDENTITY",
		"",
		"# Runtime paths",
		"Use memory path /root/.sumi/memory and workspace /root/project.",
		"",
		"# Safety",
		"GLOBAL SAFETY",
		"Read MEMORY.md from ~/.sumi before each run.",
		"Persona file is {{persona_soul_path}}.",
		"",
		"## Memory Policy",
		"WRITE IMMEDIATELY to root memory.",
	}, "\n")
	if err := os.WriteFile(globalSoul, []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(personaSoul, []byte("PERSONA OVERLAY"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &RuntimeEnv{
		Workspace:  "/current/workspace",
		MemoryRoot: "/current/memory",
		SoulPath:   globalSoul,
		Persona: &Persona{
			ID:       "debug",
			Display:  "Debug",
			SoulPath: personaSoul,
		},
	}
	s := BuildSystemPrompt(env, &Turn{Source: "desktop:channel:alpha", Session: &session.Session{}})
	for _, want := range []string{
		"GLOBAL IDENTITY",
		"GLOBAL SAFETY",
		"PERSONA OVERLAY",
		"Persona file is " + personaSoul,
		"memory_root: /current/memory",
		"workspace: /current/workspace",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("prompt missing %q:\n%s", want, s)
		}
	}
	for _, bad := range []string{
		"Runtime paths",
		"/root/.sumi/memory",
		"/root/project",
		"MEMORY.md",
		"~/.sumi",
		"Memory Policy",
		"WRITE IMMEDIATELY",
	} {
		if strings.Contains(s, bad) {
			t.Fatalf("root-private content %q leaked:\n%s", bad, s)
		}
	}
}

func TestSystemPromptKeepsFullRootSoulForDefaultSumi(t *testing.T) {
	dir := t.TempDir()
	globalSoul := filepath.Join(dir, "SOUL.md")
	root := "GLOBAL SOUL\nUse memory path /root/.sumi/memory."
	if err := os.WriteFile(globalSoul, []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	s := BuildSystemPrompt(&RuntimeEnv{SoulPath: globalSoul}, &Turn{Session: &session.Session{}})
	for _, want := range []string{"Sumi base identity (root SOUL.md):", "GLOBAL SOUL", "/root/.sumi/memory"} {
		if !strings.Contains(s, want) {
			t.Fatalf("default Sumi prompt missing %q:\n%s", want, s)
		}
	}
}
