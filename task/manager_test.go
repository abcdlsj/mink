package task

import (
	"strings"
	"sync"
	"testing"
)

type memStore struct {
	mu    sync.Mutex
	tasks map[string]Task
	runs  map[string]Run
}

func newMemStore() *memStore {
	return &memStore{tasks: map[string]Task{}, runs: map[string]Run{}}
}

func (s *memStore) SaveTask(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *t
	s.tasks[t.ID] = clone
	return nil
}

func (s *memStore) LoadTask(id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		clone := t
		return &clone, nil
	}
	return nil, nil
}

func (s *memStore) ListTasksBySpace(spaceID string) ([]*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Task, 0)
	for _, t := range s.tasks {
		if spaceID != "" && t.SpaceID != spaceID {
			continue
		}
		clone := t
		out = append(out, &clone)
	}
	return out, nil
}

func (s *memStore) SaveRun(r *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *r
	s.runs[r.ID] = clone
	return nil
}

func (s *memStore) LoadRun(id string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[id]; ok {
		clone := r
		return &clone, nil
	}
	return nil, nil
}

func (s *memStore) ListRunsByTask(taskID string) ([]*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Run, 0)
	for _, r := range s.runs {
		if r.TaskID != taskID {
			continue
		}
		clone := r
		out = append(out, &clone)
	}
	return out, nil
}

func validInput() CreateTaskInput {
	return CreateTaskInput{
		SpaceID:          "space-1",
		TriggerMessageID: "msg-1",
		InitiatorID:      "user",
		WorkerID:         "coder",
		Title:            "audit retry policy",
	}
}

func TestCreateRequiresEveryAnchor(t *testing.T) {
	m := NewManager(newMemStore())
	cases := []struct {
		name string
		in   CreateTaskInput
		want error
	}{
		{"missing space", CreateTaskInput{InitiatorID: "u", WorkerID: "w", Title: "t"}, ErrSpaceIDRequired},
		{"missing initiator", CreateTaskInput{SpaceID: "s", WorkerID: "w", Title: "t"}, ErrInitiatorRequired},
		{"missing worker", CreateTaskInput{SpaceID: "s", InitiatorID: "u", Title: "t"}, ErrWorkerRequired},
		{"missing title", CreateTaskInput{SpaceID: "s", InitiatorID: "u", WorkerID: "w"}, ErrTitleRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.Create(tc.in); err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCreateRejectsOversizedTitle(t *testing.T) {
	m := NewManager(newMemStore())
	in := validInput()
	in.Title = strings.Repeat("a", MaxTitleLen+1)
	if _, err := m.Create(in); err != ErrTitleTooLong {
		t.Fatalf("err = %v, want ErrTitleTooLong", err)
	}
}

func TestCreateThenUpdateRoundTrip(t *testing.T) {
	m := NewManager(newMemStore())
	tk, err := m.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != StatusQueued {
		t.Fatalf("status = %v", tk.Status)
	}
	if _, err := m.Update(tk.ID, UpdateTaskInput{Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Update(tk.ID, UpdateTaskInput{Status: StatusFinished, Outcome: "ok", ResultMessageID: "msg-99"}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFinished || got.Outcome != "ok" || got.ResultMessageID != "msg-99" {
		t.Fatalf("got = %+v", got)
	}
}

func TestUpdateRejectsOversizedOutcome(t *testing.T) {
	m := NewManager(newMemStore())
	tk, err := m.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", MaxOutcomeLen+1)
	if _, err := m.Update(tk.ID, UpdateTaskInput{Outcome: long}); err != ErrOutcomeTooLong {
		t.Fatalf("err = %v, want ErrOutcomeTooLong", err)
	}
}

func TestRunLifecycleAndKeyStepWhitelist(t *testing.T) {
	m := NewManager(newMemStore())
	tk, err := m.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.StartRun(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	steps := []KeyStep{
		{Kind: KindRead, Title: "Read project files", OK: true},
		{Kind: KindRun, Title: "Ran tests", OK: true},
		{Kind: KindError, Title: "Failed: timeout"},
	}
	if _, err := m.FinishRun(r.ID, FinishRunInput{Status: StatusFailed, KeySteps: steps}); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetRun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || len(got.KeySteps) != 3 {
		t.Fatalf("run = %+v", got)
	}
}

func TestKeyStepRejectsUnknownKind(t *testing.T) {
	m := NewManager(newMemStore())
	tk, _ := m.Create(validInput())
	r, _ := m.StartRun(tk.ID)
	bad := []KeyStep{{Kind: KeyStepKind("tool_call_burst"), Title: "raw"}}
	if _, err := m.FinishRun(r.ID, FinishRunInput{Status: StatusFinished, KeySteps: bad}); err != ErrKeyStepKind {
		t.Fatalf("err = %v, want ErrKeyStepKind", err)
	}
}

func TestKeyStepRejectsRawStdoutInTitle(t *testing.T) {
	m := NewManager(newMemStore())
	tk, _ := m.Create(validInput())
	r, _ := m.StartRun(tk.ID)
	long := strings.Repeat("stdout-line\n", 20)
	bad := []KeyStep{{Kind: KindRun, Title: long}}
	if _, err := m.FinishRun(r.ID, FinishRunInput{Status: StatusFinished, KeySteps: bad}); err != ErrKeyStepTitle {
		t.Fatalf("err = %v, want ErrKeyStepTitle (raw output exceeds title limit)", err)
	}
}

func TestKeyStepRejectsOverflow(t *testing.T) {
	m := NewManager(newMemStore())
	tk, _ := m.Create(validInput())
	r, _ := m.StartRun(tk.ID)
	steps := make([]KeyStep, MaxKeySteps+1)
	for i := range steps {
		steps[i] = KeyStep{Kind: KindRead, Title: "step"}
	}
	if _, err := m.FinishRun(r.ID, FinishRunInput{Status: StatusFinished, KeySteps: steps}); err != ErrKeyStepOverflow {
		t.Fatalf("err = %v, want ErrKeyStepOverflow", err)
	}
}

func TestListBySpaceFiltersBySpaceID(t *testing.T) {
	m := NewManager(newMemStore())
	in := validInput()
	in.SpaceID = "space-A"
	if _, err := m.Create(in); err != nil {
		t.Fatal(err)
	}
	in.SpaceID = "space-B"
	if _, err := m.Create(in); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListBySpace("space-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].SpaceID != "space-A" {
		t.Fatalf("space = %q", got[0].SpaceID)
	}
}

func TestConcurrentCreateUpdate(t *testing.T) {
	m := NewManager(newMemStore())
	tk, err := m.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Update(tk.ID, UpdateTaskInput{Status: StatusRunning})
		}()
	}
	wg.Wait()
	got, err := m.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("status = %v", got.Status)
	}
}
