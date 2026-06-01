package desktop

import (
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/space"
)

func TestCreateAgentDMReturnsFreshInstance(t *testing.T) {
	b, _ := newThreadBackend(t)
	first, err := b.CreateAgentDM("coder")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.PersonaID != "coder" {
		t.Fatalf("first = %+v", first)
	}
	second, err := b.CreateAgentDM("coder")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("expected new instance per call, got same id %q twice", first.ID)
	}
	if second.PersonaID != "coder" {
		t.Fatalf("second persona = %q", second.PersonaID)
	}
}

func TestCreateAgentDMRejectsUnknownPersona(t *testing.T) {
	b, _ := newThreadBackend(t)
	if _, err := b.CreateAgentDM("ghost"); err == nil {
		t.Fatal("expected error for unknown persona")
	}
}

func TestCreateAgentDMRejectsEmptyPersona(t *testing.T) {
	b, _ := newThreadBackend(t)
	if _, err := b.CreateAgentDM(""); err == nil {
		t.Fatal("expected error for empty persona")
	}
	if _, err := b.CreateAgentDM("   "); err == nil {
		t.Fatal("expected error for whitespace persona")
	}
}

func TestListAgentDMsReturnsAllInstancesNewestFirst(t *testing.T) {
	b, a := newThreadBackend(t)
	one, _ := b.CreateAgentDM("coder")
	two, _ := b.CreateAgentDM("coder")
	three, _ := b.CreateAgentDM("reviewer")
	// Bump the second AgentDM by appending a message so its UpdatedAt
	// moves ahead.
	if _, err := a.Spaces().AppendUserMessage(two.ID, "hi", nil); err != nil {
		t.Fatal(err)
	}
	got := b.ListAgentDMs()
	if len(got) < 3 {
		t.Fatalf("len = %d, want >= 3", len(got))
	}
	ids := make(map[string]bool, len(got))
	for _, item := range got {
		ids[item.ID] = true
	}
	for _, want := range []string{one.ID, two.ID, three.ID} {
		if !ids[want] {
			t.Fatalf("expected list to include %q", want)
		}
	}
	// Latest update should be the bumped one.
	if got[0].ID != two.ID {
		t.Fatalf("first row = %q, want bumped %q (sort by UpdatedAt desc)", got[0].ID, two.ID)
	}
}

func TestGetAgentDMResolvesBySpaceID(t *testing.T) {
	b, a := newThreadBackend(t)
	created, err := b.CreateAgentDM("coder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(created.ID, "hi", nil); err != nil {
		t.Fatal(err)
	}
	detail := b.GetAgentDM(created.ID)
	if detail.Item.ID != created.ID {
		t.Fatalf("Item.ID = %q, want %q", detail.Item.ID, created.ID)
	}
	if !strings.Contains(detail.Item.Title, "Coder") && !strings.Contains(detail.Item.Title, "New chat") {
		t.Fatalf("Title = %q, expected persona display or 'New chat'", detail.Item.Title)
	}
	// Two new instances must not share their message history.
	other, err := b.CreateAgentDM("coder")
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == created.ID {
		t.Fatal("regression: new instance shares space id")
	}
	if _, err := a.Spaces().AppendUserMessage(other.ID, "fresh", nil); err != nil {
		t.Fatal(err)
	}
	d1 := b.GetAgentDM(created.ID)
	d2 := b.GetAgentDM(other.ID)
	if d1.Item.ID == d2.Item.ID {
		t.Fatal("two AgentDMs share Space ID")
	}
	d1Has := false
	for _, m := range d1.Messages {
		if m.Content == "fresh" {
			d1Has = true
		}
	}
	if d1Has {
		t.Fatal("instance 1 should not see messages from instance 2")
	}
}

func TestAgentDMSourceWithSpaceIDResolvesPersona(t *testing.T) {
	b, a := newThreadBackend(t)
	item, err := b.CreateAgentDM("coder")
	if err != nil {
		t.Fatal(err)
	}
	source := "desktop:agent:" + item.ID
	target := space.MapSource(source)
	if target.Kind != space.KindAgentDM {
		t.Fatalf("MapSource(%q).Kind = %v, want KindAgentDM", source, target.Kind)
	}
	if target.Seed != item.ID {
		t.Fatalf("MapSource(%q).Seed = %q, want %q", source, target.Seed, item.ID)
	}
	_ = a
}