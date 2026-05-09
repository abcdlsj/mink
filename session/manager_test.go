package session

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestAcquireTurnSerializesSameSession(t *testing.T) {
	m := NewManager(newMemStore())
	const id = "sess-1"

	var active int32
	var peak int32
	var queued int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release := m.AcquireTurn(id, func(int) { atomic.AddInt32(&queued, 1) })
			defer release()
			n := atomic.AddInt32(&active, 1)
			if n > atomic.LoadInt32(&peak) {
				atomic.StoreInt32(&peak, n)
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got != 1 {
		t.Fatalf("peak concurrent holders = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&queued); got == 0 {
		t.Fatalf("onQueue never fired, want at least one queued caller")
	}
	if d := m.TurnDepth(id); d != 0 {
		t.Fatalf("turn depth after release = %d, want 0", d)
	}
}

func TestAcquireTurnDifferentSessionsRunConcurrently(t *testing.T) {
	m := NewManager(newMemStore())

	r1 := m.AcquireTurn("a", nil)
	defer r1()
	done := make(chan struct{})
	go func() {
		r2 := m.AcquireTurn("b", nil)
		r2()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("acquiring a different session blocked on unrelated lock")
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
