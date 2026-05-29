package space

import (
	"testing"
	"time"
)

func TestNewIDIsKindTagged(t *testing.T) {
	id := NewID(KindChannel, "sumi", time.Now())
	if id == "" {
		t.Fatal("NewID returned empty")
	}
	if got := len(id); got < 18 {
		t.Errorf("NewID looks too short: %q", id)
	}
}

func TestSpaceParticipantSetIsIdempotent(t *testing.T) {
	sp := New(KindChannel, "#sumi", []Participant{
		{ID: "user", Kind: ParticipantUser, Display: "You", Status: StatusAvailable, JoinedAt: time.Now()},
	})
	added := sp.AddParticipant(Participant{ID: "coder", Kind: ParticipantAgent, Display: "Coder"})
	if !added {
		t.Error("first AddParticipant should mutate")
	}
	again := sp.AddParticipant(Participant{ID: "coder", Kind: ParticipantAgent, Display: "Coder"})
	if again {
		t.Error("repeat AddParticipant should be a no-op")
	}
	if len(sp.Participants) != 2 {
		t.Errorf("participants = %d, want 2", len(sp.Participants))
	}
	if sp.Participants[1].Status != StatusAvailable {
		t.Errorf("default status should be available, got %q", sp.Participants[1].Status)
	}
	if sp.Participants[1].JoinedAt.IsZero() {
		t.Error("joined_at should be stamped")
	}
}

func TestSpaceAddMessageStampsAndOrders(t *testing.T) {
	sp := New(KindChannel, "#sumi", nil)
	m1 := sp.AddMessage(Message{AuthorID: "user", AuthorKind: ParticipantUser, Content: "hi"})
	m2 := sp.AddMessage(Message{AuthorID: "coder", AuthorKind: ParticipantAgent, Content: "yo", ParentMessageID: m1.ID})
	if m1.ID == "" || m2.ID == "" {
		t.Fatal("AddMessage should fill missing ids")
	}
	if m1.SpaceID != sp.ID || m2.SpaceID != sp.ID {
		t.Error("AddMessage should stamp space_id")
	}
	if m1.CreatedAt.IsZero() || m2.CreatedAt.IsZero() {
		t.Error("AddMessage should fill created_at")
	}
	replies := sp.Replies(m1.ID)
	if len(replies) != 1 || replies[0].ID != m2.ID {
		t.Errorf("Replies(parent) = %v, want one matching child", replies)
	}
	if got := sp.Replies(""); got != nil {
		t.Errorf("Replies(\"\") should be nil, got %v", got)
	}
	if sp.UpdatedAt.IsZero() || sp.UpdatedAt.Before(sp.CreatedAt) {
		t.Error("UpdatedAt should advance on writes")
	}
}
