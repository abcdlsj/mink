package task

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
	store Store
	mu    sync.Mutex
}

func NewManager(store Store) *Manager {
	return &Manager{store: store}
}

type CreateTaskInput struct {
	SpaceID          string
	TriggerMessageID string
	InitiatorID      string
	WorkerID         string
	Title            string
	Source           string
}

func (m *Manager) Create(in CreateTaskInput) (*Task, error) {
	now := time.Now()
	t := &Task{
		ID:               NewID(),
		SpaceID:          in.SpaceID,
		TriggerMessageID: in.TriggerMessageID,
		InitiatorID:      in.InitiatorID,
		WorkerID:         in.WorkerID,
		Title:            strings.TrimSpace(in.Title),
		Source:           in.Source,
		Status:           StatusQueued,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := ValidateTask(*t); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.SaveTask(t); err != nil {
		return nil, err
	}
	return t, nil
}

type UpdateTaskInput struct {
	Status          Status
	Outcome         string
	ResultMessageID string
}

func (m *Manager) Update(id string, in UpdateTaskInput) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.store.LoadTask(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrTaskNotFound
	}
	if in.Status != "" {
		t.Status = in.Status
	}
	if in.Outcome != "" {
		if runeLen(in.Outcome) > MaxOutcomeLen {
			return nil, ErrOutcomeTooLong
		}
		t.Outcome = in.Outcome
	}
	if in.ResultMessageID != "" {
		t.ResultMessageID = in.ResultMessageID
	}
	t.UpdatedAt = time.Now()
	if err := m.store.SaveTask(t); err != nil {
		return nil, err
	}
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

func (m *Manager) StartRun(taskID string) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.store.LoadTask(taskID); err != nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	r := &Run{
		ID:        NewRunID(),
		TaskID:    taskID,
		StartedAt: time.Now(),
		Status:    StatusRunning,
	}
	if err := m.store.SaveRun(r); err != nil {
		return nil, err
	}
	return r, nil
}

type FinishRunInput struct {
	Status   Status
	KeySteps []KeyStep
}

func (m *Manager) FinishRun(id string, in FinishRunInput) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.store.LoadRun(id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrTaskNotFound
	}
	if err := ValidateKeySteps(in.KeySteps); err != nil {
		return nil, err
	}
	r.Status = in.Status
	r.EndedAt = time.Now()
	r.KeySteps = append([]KeyStep(nil), in.KeySteps...)
	if err := m.store.SaveRun(r); err != nil {
		return nil, err
	}
	return r, nil
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
