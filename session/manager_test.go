package session

import "testing"

type memStore struct {
	sessions map[string]*Session
	current  map[string]string
}

func newMemStore() *memStore {
	return &memStore{
		sessions: map[string]*Session{},
		current:  map[string]string{},
	}
}

func (s *memStore) SaveSession(v *Session) error {
	s.sessions[v.ID] = v
	return nil
}

func (s *memStore) LoadSession(id string) (*Session, error) {
	v, ok := s.sessions[id]
	if !ok {
		return nil, errMissingSession(id)
	}
	return v, nil
}

func (s *memStore) ListSessions() ([]*Session, error) {
	out := make([]*Session, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	return out, nil
}

func (s *memStore) CurrentSessionID(source string) (string, error) {
	return s.current[source], nil
}

func (s *memStore) SetCurrentSession(source, id string) error {
	s.current[source] = id
	return nil
}

func TestListBySourceFiltersSessions(t *testing.T) {
	st := newMemStore()
	m := NewManager(st)

	cli := New("cli")
	tg := New("telegram:42")
	_ = st.SaveSession(cli)
	_ = st.SaveSession(tg)

	got, err := m.ListBySource("cli")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions", len(got))
	}
	if got[0].ID != cli.ID {
		t.Fatalf("got session %q", got[0].ID)
	}
}

func TestSwitchRejectsDifferentSource(t *testing.T) {
	st := newMemStore()
	m := NewManager(st)

	tg := New("telegram:42")
	_ = st.SaveSession(tg)

	if _, err := m.Switch("cli", tg.ID); err == nil {
		t.Fatal("expected source mismatch error")
	}
}

func errMissingSession(id string) error {
	return &missingSessionError{id: id}
}

type missingSessionError struct {
	id string
}

func (e *missingSessionError) Error() string {
	return "session not found: " + e.id
}
