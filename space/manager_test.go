package space

import (
	"testing"
)

// memoryStore implements space.Store for tests.
type memoryStore struct {
	byID map[string]*Space
}

func newMemoryStore() *memoryStore { return &memoryStore{byID: map[string]*Space{}} }

func (m *memoryStore) SaveSpace(sp *Space) error {
	cp := *sp
	m.byID[sp.ID] = &cp
	return nil
}

func (m *memoryStore) LoadSpace(id string) (*Space, error) {
	sp, ok := m.byID[id]
	if !ok {
		return nil, errSpaceNotFound
	}
	cp := *sp
	return &cp, nil
}

func (m *memoryStore) ListSpaces() ([]*Space, error) {
	out := make([]*Space, 0, len(m.byID))
	for _, sp := range m.byID {
		cp := *sp
		out = append(out, &cp)
	}
	return out, nil
}

func (m *memoryStore) FindSpaceByKindAndSeed(kind Kind, seed string) (*Space, error) {
	for _, sp := range m.byID {
		if sp.Kind == kind && sp.Title == seed {
			cp := *sp
			return &cp, nil
		}
	}
	return nil, nil
}

var errSpaceNotFound = errMockNotFound{}

type errMockNotFound struct{}

func (errMockNotFound) Error() string { return "space not found" }

func TestMapSource(t *testing.T) {
	cases := []struct {
		in        string
		wantKind  Kind
		wantSeed  string
		wantEmpty bool
	}{
		{"desktop", KindChannel, "default", false},
		{"", KindChannel, "default", false},
		{"desktop:agent:coder", KindAgentDM, "coder", false},
		{"desktop:agent:coder:persona:coder", KindAgentDM, "coder", false},
		{"cli", KindDirectChat, "cli", false},
		{"tg:42", KindDirectChat, "tg:42", false},
		{"subtask:task-abc", "", "", true},
		{"random-other", KindDirectChat, "random-other", false},
	}
	for _, c := range cases {
		got := MapSource(c.in)
		if c.wantEmpty {
			if got.Kind != "" {
				t.Errorf("MapSource(%q) should produce empty target, got %+v", c.in, got)
			}
			continue
		}
		if got.Kind != c.wantKind || got.Seed != c.wantSeed {
			t.Errorf("MapSource(%q) = %+v, want kind=%s seed=%s", c.in, got, c.wantKind, c.wantSeed)
		}
	}
}

func TestEnsureSpaceSeedsParticipantsCorrectly(t *testing.T) {
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")

	// Channel: user only
	ch, err := mgr.EnsureSpace(KindChannel, "default", PersonaInfo{})
	if err != nil {
		t.Fatalf("ensure channel: %v", err)
	}
	if len(ch.Participants) != 1 || ch.Participants[0].Kind != ParticipantUser {
		t.Errorf("channel seed should be user-only, got %+v", ch.Participants)
	}

	// Agent DM: user + that agent (rejects empty agent id)
	if _, err := mgr.EnsureSpace(KindAgentDM, "coder", PersonaInfo{}); err == nil {
		t.Error("agent_dm with empty agent should error")
	}
	dm, err := mgr.EnsureSpace(KindAgentDM, "coder", PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatalf("ensure agent_dm: %v", err)
	}
	if len(dm.Participants) != 2 {
		t.Fatalf("agent_dm participants = %d, want 2", len(dm.Participants))
	}
	if dm.Participants[0].Kind != ParticipantUser || dm.Participants[1].ID != "coder" {
		t.Errorf("agent_dm seed wrong: %+v", dm.Participants)
	}

	// EnsureSpace is idempotent
	again, _ := mgr.EnsureSpace(KindAgentDM, "coder", PersonaInfo{ID: "coder"})
	if again.ID != dm.ID {
		t.Error("EnsureSpace should return the existing space, not create a new one")
	}
}

func TestAppendMessageRejectsMissingAuthor(t *testing.T) {
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")
	ch, _ := mgr.EnsureSpace(KindChannel, "default", PersonaInfo{})

	// User message with non-empty user id always works.
	if _, err := mgr.AppendUserMessage(ch.ID, "hi", nil); err != nil {
		t.Errorf("AppendUserMessage failed: %v", err)
	}

	// Agent message with empty id is rejected (Iris's hard rule).
	if _, err := mgr.AppendAgentMessage(ch.ID, PersonaInfo{ID: ""}, "...", "", nil, ""); err == nil {
		t.Error("agent message with empty id should be rejected")
	}

	// Agent message with valid persona accepted.
	if _, err := mgr.AppendAgentMessage(ch.ID, PersonaInfo{ID: "coder", Display: "Coder"}, "ok", "", nil, ""); err != nil {
		t.Errorf("agent message with valid persona failed: %v", err)
	}

	loaded, _ := store.LoadSpace(ch.ID)
	if len(loaded.Messages) != 2 {
		t.Errorf("messages saved = %d, want 2", len(loaded.Messages))
	}
}

func TestEnsureForSourceRoundTrips(t *testing.T) {
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")

	// desktop -> default channel
	sp, err := mgr.EnsureForSource("desktop", PersonaInfo{})
	if err != nil || sp.Kind != KindChannel {
		t.Errorf("desktop should map to channel, got %v / %v", sp, err)
	}

	// desktop:agent:reviewer -> agent_dm reviewer
	sp, err = mgr.EnsureForSource("desktop:agent:reviewer", PersonaInfo{ID: "reviewer", Display: "Reviewer"})
	if err != nil || sp.Kind != KindAgentDM || sp.Title != "reviewer" {
		t.Errorf("agent source mapping wrong: %+v / %v", sp, err)
	}

	// subtask:* must error rather than silently land in a Space.
	if _, err := mgr.EnsureForSource("subtask:task-1", PersonaInfo{}); err == nil {
		t.Error("subtask source should not produce a Space")
	}
}

// failingStore wraps memoryStore and rejects the next SaveSpace call.
type failingStore struct {
	*memoryStore
	failNext bool
	failErr  error
}

func newFailingStore() *failingStore {
	return &failingStore{memoryStore: newMemoryStore()}
}

func (f *failingStore) SaveSpace(sp *Space) error {
	if f.failNext {
		f.failNext = false
		return f.failErr
	}
	return f.memoryStore.SaveSpace(sp)
}

func TestAppendMessageWithRoutingAtomicAdd(t *testing.T) {
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")
	ch, _ := mgr.EnsureSpace(KindChannel, "default", PersonaInfo{})

	draft := Message{
		AuthorID:   "user",
		AuthorKind: ParticipantUser,
		Content:    "@coder look",
		Mentions:   []string{"coder"},
	}
	msg, added, err := mgr.AppendMessageWithRouting(ch.ID, draft, []string{"coder"}, func(id string) PersonaInfo {
		return PersonaInfo{ID: id, Display: "Coder"}
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if msg.ID == "" {
		t.Error("written message id should be set")
	}
	if !equalSets(added, []string{"coder"}) {
		t.Errorf("added participants = %v, want [coder]", added)
	}
	loaded, _ := store.LoadSpace(ch.ID)
	if !loaded.HasParticipant("coder") {
		t.Error("coder should now be a participant")
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("messages = %d, want 1", len(loaded.Messages))
	}
}

func TestAppendMessageWithRoutingNoOpForExistingParticipant(t *testing.T) {
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")
	ch, _ := mgr.EnsureSpace(KindAgentDM, "coder", PersonaInfo{ID: "coder", Display: "Coder"})

	draft := Message{AuthorID: "user", AuthorKind: ParticipantUser, Content: "again @coder"}
	_, added, err := mgr.AppendMessageWithRouting(ch.ID, draft, []string{"coder"}, nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("already-present coder should not be re-added, got %v", added)
	}
	loaded, _ := store.LoadSpace(ch.ID)
	count := 0
	for _, p := range loaded.Participants {
		if p.ID == "coder" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("coder appears %d times in participants, want 1", count)
	}
}

func TestAppendMessageWithRoutingDropsUnknownIds(t *testing.T) {
	// "unknown" here means the routing layer passes an id but the
	// resolver maps it to an empty/blank id; we still need to make
	// sure the manager doesn't insert garbage.
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")
	ch, _ := mgr.EnsureSpace(KindChannel, "default", PersonaInfo{})

	draft := Message{AuthorID: "user", AuthorKind: ParticipantUser, Content: "test"}
	_, added, err := mgr.AppendMessageWithRouting(ch.ID, draft, []string{"   ", ""}, nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("blank ids should be ignored, got %v", added)
	}
	loaded, _ := store.LoadSpace(ch.ID)
	if len(loaded.Participants) != 1 {
		t.Errorf("only the user seed should remain, got %d", len(loaded.Participants))
	}
}

func TestAppendMessageWithRoutingRejectsBadAuthor(t *testing.T) {
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")
	ch, _ := mgr.EnsureSpace(KindChannel, "default", PersonaInfo{})

	if _, _, err := mgr.AppendMessageWithRouting(ch.ID, Message{AuthorKind: ParticipantUser}, nil, nil); err == nil {
		t.Error("missing AuthorID should error")
	}
	if _, _, err := mgr.AppendMessageWithRouting(ch.ID, Message{AuthorID: "user"}, nil, nil); err == nil {
		t.Error("missing AuthorKind should error")
	}
}

func TestAppendMessageWithRoutingNoHalfWriteOnSaveFail(t *testing.T) {
	failing := newFailingStore()
	mgr := NewManager(failing, "user", "You")
	// Seed the channel through the failing store but with success.
	ch, _ := mgr.EnsureSpace(KindChannel, "default", PersonaInfo{})

	failing.failNext = true
	failing.failErr = errMockNotFound{}

	draft := Message{AuthorID: "user", AuthorKind: ParticipantUser, Content: "@coder hi"}
	_, _, err := mgr.AppendMessageWithRouting(ch.ID, draft, []string{"coder"}, nil)
	if err == nil {
		t.Fatal("expected save failure to propagate")
	}
	loaded, _ := failing.LoadSpace(ch.ID)
	if loaded.HasParticipant("coder") {
		t.Error("save failure should not have left coder in participants")
	}
	if len(loaded.Messages) != 0 {
		t.Errorf("save failure should not have left a message, got %d", len(loaded.Messages))
	}
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ma := map[string]int{}
	for _, x := range a {
		ma[x]++
	}
	for _, x := range b {
		ma[x]--
	}
	for _, v := range ma {
		if v != 0 {
			return false
		}
	}
	return true
}
