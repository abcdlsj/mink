package task

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/bus"
)

var ErrTaskNotFound = errors.New("task: not found")

type Store interface {
	SaveTask(*Task) error
	LoadTask(id string) (*Task, error)
	ListTasksBySpace(spaceID string) ([]*Task, error)
	SaveRun(*Run) error
	LoadRun(id string) (*Run, error)
	ListRunsByTask(taskID string) ([]*Run, error)
}

type Manager struct {
	store  Store
	mu     sync.Mutex
	events func(bus.Event)
}

func NewManager(store Store) *Manager {
	return &Manager{store: store}
}

func (m *Manager) SetEventSink(fn func(bus.Event)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = fn
}

func (m *Manager) publish(ev bus.Event) {
	if m == nil || m.events == nil {
		return
	}
	m.events(ev)
}

type CreateTaskInput struct {
	SpaceID            string
	TriggerMessageID   string
	SourceThreadID     string
	InitiatorID        string
	CreatedBy          string
	WorkerID           string
	AssignedBy         string
	Title              string
	ExpectedOutcome    string
	AcceptanceCriteria string
	Source             string
	State              TaskState
}

func (m *Manager) Create(in CreateTaskInput) (*Task, error) {
	now := time.Now()
	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		createdBy = strings.TrimSpace(in.InitiatorID)
	}
	assignedBy := strings.TrimSpace(in.AssignedBy)
	if assignedBy == "" {
		assignedBy = createdBy
	}
	t := &Task{
		ID:                 NewID(),
		SpaceID:            strings.TrimSpace(in.SpaceID),
		TriggerMessageID:   strings.TrimSpace(in.TriggerMessageID),
		SourceThreadID:     strings.TrimSpace(in.SourceThreadID),
		InitiatorID:        strings.TrimSpace(in.InitiatorID),
		CreatedBy:          createdBy,
		WorkerID:           strings.TrimSpace(in.WorkerID),
		AssignedBy:         assignedBy,
		Title:              strings.TrimSpace(in.Title),
		ExpectedOutcome:    strings.TrimSpace(in.ExpectedOutcome),
		AcceptanceCriteria: strings.TrimSpace(in.AcceptanceCriteria),
		Source:             strings.TrimSpace(in.Source),
		State:              cleanState(in.State),
		Status:             StatusQueued,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := ValidateTask(*t); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if err := m.store.SaveTask(t); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:      bus.TaskCreated,
		TaskID:    t.ID,
		SpaceID:   t.SpaceID,
		MessageID: t.TriggerMessageID,
		AgentID:   t.WorkerID,
		Source:    t.Source,
		Text:      string(t.Status),
	})
	return t, nil
}

type UpdateTaskInput struct {
	Status             Status
	WorkerID           string
	AssignedBy         string
	ExpectedOutcome    string
	AcceptanceCriteria string
	Outcome            string
	ResultMessageID    string
	State              *TaskState
}

func (m *Manager) Update(id string, in UpdateTaskInput) (*Task, error) {
	m.mu.Lock()
	t, err := m.store.LoadTask(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if t == nil {
		m.mu.Unlock()
		return nil, ErrTaskNotFound
	}
	if in.Status != "" {
		t.Status = in.Status
	}
	if strings.TrimSpace(in.WorkerID) != "" {
		t.WorkerID = strings.TrimSpace(in.WorkerID)
	}
	if strings.TrimSpace(in.AssignedBy) != "" {
		t.AssignedBy = strings.TrimSpace(in.AssignedBy)
	}
	if strings.TrimSpace(in.ExpectedOutcome) != "" {
		if runeLen(in.ExpectedOutcome) > MaxExpectedOutcomeLen {
			m.mu.Unlock()
			return nil, ErrExpectedTooLong
		}
		t.ExpectedOutcome = strings.TrimSpace(in.ExpectedOutcome)
	}
	if strings.TrimSpace(in.AcceptanceCriteria) != "" {
		if runeLen(in.AcceptanceCriteria) > MaxAcceptanceCriteriaLen {
			m.mu.Unlock()
			return nil, ErrCriteriaTooLong
		}
		t.AcceptanceCriteria = strings.TrimSpace(in.AcceptanceCriteria)
	}
	if in.Outcome != "" {
		if runeLen(in.Outcome) > MaxOutcomeLen {
			m.mu.Unlock()
			return nil, ErrOutcomeTooLong
		}
		t.Outcome = in.Outcome
	}
	if in.ResultMessageID != "" {
		t.ResultMessageID = in.ResultMessageID
	}
	if in.State != nil {
		t.State = cleanState(*in.State)
	}
	if strings.TrimSpace(t.CreatedBy) == "" {
		t.CreatedBy = strings.TrimSpace(t.InitiatorID)
	}
	if strings.TrimSpace(t.AssignedBy) == "" {
		t.AssignedBy = strings.TrimSpace(t.CreatedBy)
	}
	if err := ValidateTask(*t); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	t.UpdatedAt = time.Now()
	if err := m.store.SaveTask(t); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:      bus.TaskUpdated,
		TaskID:    t.ID,
		SpaceID:   t.SpaceID,
		MessageID: t.TriggerMessageID,
		AgentID:   t.WorkerID,
		Source:    t.Source,
		Text:      string(t.Status),
		Output:    t.Outcome,
	})
	return t, nil
}

func (m *Manager) Get(id string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.store.LoadTask(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrTaskNotFound
	}
	return t, nil
}

func (m *Manager) Cancel(id string) error {
	_, err := m.Update(id, UpdateTaskInput{Status: StatusCanceled})
	return err
}

func (m *Manager) ListBySpace(spaceID string) ([]*Task, error) {
	if strings.TrimSpace(spaceID) == "" {
		return nil, ErrSpaceIDRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.ListTasksBySpace(spaceID)
}

func (m *Manager) ListAll() ([]*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.ListTasksBySpace("")
}

func (m *Manager) StartRun(taskID string, state ...TaskState) (*Run, error) {
	m.mu.Lock()
	tk, err := m.store.LoadTask(taskID)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if tk == nil {
		m.mu.Unlock()
		return nil, ErrTaskNotFound
	}
	r := &Run{
		ID:        NewRunID(),
		TaskID:    taskID,
		StartedAt: time.Now(),
		Status:    StatusRunning,
	}
	if len(state) > 0 {
		r.State = cleanState(state[0])
	}
	if err := m.store.SaveRun(r); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:      bus.RunStarted,
		TaskID:    taskID,
		RunID:     r.ID,
		SpaceID:   tk.SpaceID,
		MessageID: tk.TriggerMessageID,
		AgentID:   tk.WorkerID,
		Source:    tk.Source,
		Text:      string(r.Status),
	})
	return r, nil
}

type FinishRunInput struct {
	Status   Status
	KeySteps []KeyStep
	State    *TaskState
}

func (m *Manager) FinishRun(id string, in FinishRunInput) (*Run, error) {
	m.mu.Lock()
	r, err := m.store.LoadRun(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if r == nil {
		m.mu.Unlock()
		return nil, ErrTaskNotFound
	}
	tk, err := m.store.LoadTask(r.TaskID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if tk == nil {
		m.mu.Unlock()
		return nil, ErrTaskNotFound
	}
	if err := ValidateKeySteps(in.KeySteps); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	r.Status = in.Status
	r.EndedAt = time.Now()
	r.KeySteps = append([]KeyStep(nil), in.KeySteps...)
	if in.State != nil {
		r.State = cleanState(*in.State)
	}
	if err := m.store.SaveRun(r); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:      bus.RunFinished,
		TaskID:    r.TaskID,
		RunID:     r.ID,
		SpaceID:   tk.SpaceID,
		MessageID: tk.TriggerMessageID,
		AgentID:   tk.WorkerID,
		Source:    tk.Source,
		Text:      string(r.Status),
	})
	return r, nil
}

func cleanState(s TaskState) TaskState {
	s.Goal = strings.TrimSpace(s.Goal)
	s.Checkpoint = strings.TrimSpace(s.Checkpoint)
	s.Todo = cleanList(s.Todo)
	s.Artifacts = cleanList(s.Artifacts)
	s.Blockers = cleanList(s.Blockers)
	s.RelatedIDs = cleanList(s.RelatedIDs)
	return s
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (m *Manager) GetRun(id string) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.store.LoadRun(id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrTaskNotFound
	}
	return r, nil
}

func (m *Manager) ListRuns(taskID string) ([]*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.ListRunsByTask(taskID)
}
