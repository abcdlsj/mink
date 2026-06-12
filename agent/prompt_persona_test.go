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
	if !strings.Contains(s, "Sumi base identity (root SOUL.md):") {
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
	if !strings.Contains(s, "Sumi base identity (root SOUL.md):") {
		t.Fatalf("prompt missing root SOUL section:\n%s", s)
	}
	if strings.Contains(s, "Persona soul overlay") {
		t.Fatalf("unexpected persona SOUL section:\n%s", s)
	}
}
