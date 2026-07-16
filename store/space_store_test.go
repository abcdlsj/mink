package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/space"
)

func TestSaveAndLoadSpaceRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	sp := space.New(space.KindAgentDM, "@coder", []space.Participant{
		{ID: "user", Kind: space.ParticipantUser, Display: "You"},
		{ID: "coder", Kind: space.ParticipantAgent, Display: "Coder"},
	})
	sp.AddMessage(space.Message{
		AuthorID: "user", AuthorKind: space.ParticipantUser, Content: "hello",
	})

	if err := s.SaveSpace(sp); err != nil {
		t.Fatalf("SaveSpace: %v", err)
	}

	got, err := s.LoadSpace(sp.ID)
	if err != nil {
		t.Fatalf("LoadSpace: %v", err)
	}
	if got.Kind != space.KindAgentDM {
		t.Errorf("kind round-trip = %q, want agent_dm", got.Kind)
	}
	if len(got.Participants) != 2 {
		t.Errorf("participants round-trip = %d, want 2", len(got.Participants))
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "hello" {
		t.Errorf("messages round-trip = %+v", got.Messages)
	}

	wantDir := filepath.Join(root, "spaces", "agent_dm")
	if !dirExists(wantDir) {
		t.Errorf("expected kind directory %q to exist", wantDir)
	}
}

func TestListSpacesEmpty(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := s.ListSpaces()
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListSpaces empty = %d, want 0", len(got))
	}
}

func TestFindSpaceByKindAndKey(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root)
	for i, kind := range []space.Kind{space.KindChannel, space.KindAgentDM} {
		key := "seed-" + string(rune('a'+i))
		sp := space.NewKeyed(kind, key, key, nil)
		_ = s.SaveSpace(sp)
	}
	hit, err := s.FindSpaceByKindAndKey(space.KindChannel, "seed-a")
	if err != nil || hit == nil {
		t.Fatalf("expected to find channel seed-a, got %v / %v", hit, err)
	}
	miss, err := s.FindSpaceByKindAndKey(space.KindChannel, "missing")
	if err != nil || miss != nil {
		t.Errorf("expected nil miss, got %v / %v", miss, err)
	}
	_ = time.Now()
}

func TestDeleteSpaceRemovesSpaceFile(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sp := space.New(space.KindDirectChat, "chat", nil)
	if err := s.SaveSpace(sp); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSpace(sp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSpace(sp.ID); err == nil {
		t.Fatalf("LoadSpace(%s) succeeded after delete", sp.ID)
	}
}
