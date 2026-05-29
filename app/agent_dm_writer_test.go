package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

func newAgentDMTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if _, err := a.Personas().Create("tshoot", persona.Meta{Display: "Tshoot", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestResolveAgentDMPersonaIDAcceptsConsistentInputs(t *testing.T) {
	a := newAgentDMTestApp(t)
	cases := []struct {
		name     string
		source   string
		explicit string
		wantID   string
	}{
		{"pc both consistent", "desktop:agent:tshoot", "tshoot", "tshoot"},
		{"pc seed only", "desktop:agent:tshoot", "", "tshoot"},
		{"cli both consistent", "cli:agent:tshoot", "tshoot", "tshoot"},
		{"cli seed only", "cli:agent:tshoot", "", "tshoot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, info, err := a.resolveAgentDMPersonaID(c.source, c.explicit)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if id != c.wantID {
				t.Fatalf("id = %q, want %q", id, c.wantID)
			}
			if info == nil || info.ID != c.wantID {
				t.Fatalf("info = %#v", info)
			}
		})
	}
}

func TestResolveAgentDMPersonaIDRejectsConflict(t *testing.T) {
	a := newAgentDMTestApp(t)
	cases := []struct {
		name     string
		source   string
		explicit string
	}{
		{"pc seed coder explicit tshoot", "desktop:agent:coder", "tshoot"},
		{"cli seed coder explicit tshoot", "cli:agent:coder", "tshoot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := a.resolveAgentDMPersonaID(c.source, c.explicit); !errors.Is(err, ErrAgentDMPersonaConflict) {
				t.Fatalf("err = %v, want ErrAgentDMPersonaConflict", err)
			}
		})
	}
}

func TestResolveAgentDMPersonaIDRejectsNonAgentDMSource(t *testing.T) {
	a := newAgentDMTestApp(t)
	cases := []string{"desktop", "cli", "desktop:direct:abc", "tg:dm:42", "tg:channel:42", "subtask:x", "scratch:y"}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			if _, _, err := a.resolveAgentDMPersonaID(src, "tshoot"); !errors.Is(err, ErrNotAgentDMSource) {
				t.Fatalf("err = %v, want ErrNotAgentDMSource", err)
			}
		})
	}
}

func TestResolveAgentDMPersonaIDRejectsUnknownPersona(t *testing.T) {
	a := newAgentDMTestApp(t)
	cases := []struct {
		name     string
		source   string
		explicit string
	}{
		{"pc seed ghost", "desktop:agent:ghost", ""},
		{"cli seed ghost", "cli:agent:ghost", ""},
		{"pc explicit ghost", "desktop:agent:ghost", "ghost"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := a.resolveAgentDMPersonaID(c.source, c.explicit); !errors.Is(err, ErrAgentDMPersonaNotFound) {
				t.Fatalf("err = %v, want ErrAgentDMPersonaNotFound", err)
			}
		})
	}
}

func TestAppendAgentDMUserToSpaceWritesAndIsIdempotentSource(t *testing.T) {
	a := newAgentDMTestApp(t)
	written, err := a.appendAgentDMUserToSpace("desktop:agent:tshoot", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if written.AuthorKind != space.ParticipantUser {
		t.Fatalf("kind = %v", written.AuthorKind)
	}
	w2, err := a.appendAgentDMUserToSpace("cli:agent:tshoot", "", "again")
	if err != nil {
		t.Fatal(err)
	}
	if w2.SpaceID != written.SpaceID {
		t.Fatalf("CLI and PC AgentDM for same persona must share Space, got %q vs %q", w2.SpaceID, written.SpaceID)
	}
	sp, err := a.Spaces().LoadSpace(written.SpaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(sp.Messages))
	}
}

func TestAppendAgentDMAssistantToSpaceWritesWorkerAuthor(t *testing.T) {
	a := newAgentDMTestApp(t)
	if _, err := a.appendAgentDMUserToSpace("desktop:agent:coder", "coder", "look at retry"); err != nil {
		t.Fatal(err)
	}
	written, err := a.appendAgentDMAssistantToSpace("desktop:agent:coder", "coder", "looking", "thinking", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if written.AuthorID != "coder" || written.AuthorKind != space.ParticipantAgent {
		t.Fatalf("author = %q/%v, want coder/agent", written.AuthorID, written.AuthorKind)
	}
	sp, err := a.Spaces().LoadSpace(written.SpaceID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range sp.Messages {
		if m.AuthorID == "coder" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("worker-authored count = %d, want 1", count)
	}
}

func TestAppendAgentDMRefusesNonAgentDM(t *testing.T) {
	a := newAgentDMTestApp(t)
	if _, err := a.appendAgentDMUserToSpace("desktop", "tshoot", "hi"); !errors.Is(err, ErrNotAgentDMSource) {
		t.Fatalf("user err = %v, want ErrNotAgentDMSource", err)
	}
	if _, err := a.appendAgentDMAssistantToSpace("desktop", "tshoot", "x", "", nil, ""); !errors.Is(err, ErrNotAgentDMSource) {
		t.Fatalf("assistant err = %v, want ErrNotAgentDMSource", err)
	}
}
