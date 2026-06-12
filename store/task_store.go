package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abcdlsj/sumi/task"
)

func (s *Store) SaveTask(t *task.Task) error {
	if t == nil {
		return fmt.Errorf("task is nil")
	}
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("task id is empty")
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeFile(s.taskPath(t.ID), append(data, '\n'))
}

func (s *Store) LoadTask(id string) (*task.Task, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("task id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.taskPath(id)
	if !fileExists(path) {
		return nil, nil
	}
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var t task.Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) ListTasksBySpace(spaceID string) ([]*task.Task, error) {
	spaceID = strings.TrimSpace(spaceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*task.Task, 0)
	err := walkDirJSON(s.tasksDir, func(path string) error {
		data, err := readFile(path)
		if err != nil {
			return nil
		}
		var t task.Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil
		}
		if spaceID != "" && t.SpaceID != spaceID {
			return nil
		}
		out = append(out, &t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) DeleteTasksBySpace(spaceID string) (int, error) {
	spaceID = strings.TrimSpace(spaceID)
	if spaceID == "" {
		return 0, nil
	}
	return s.deleteTasksMatching(func(t task.Task) bool {
		return t.SpaceID == spaceID
	})
}

func (s *Store) DeleteTasksByThread(spaceID, sourceThreadID string) (int, error) {
	spaceID = strings.TrimSpace(spaceID)
	sourceThreadID = strings.TrimSpace(sourceThreadID)
	if spaceID == "" || sourceThreadID == "" {
		return 0, nil
	}
	return s.deleteTasksMatching(func(t task.Task) bool {
		return t.SpaceID == spaceID && t.SourceThreadID == sourceThreadID
	})
}

func (s *Store) deleteTasksMatching(match func(task.Task) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type hit struct {
		taskID string
		path   string
	}
	var hits []hit
	if err := walkDirJSON(s.tasksDir, func(path string) error {
		data, err := readFile(path)
		if err != nil {
			return nil
		}
		var t task.Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil
		}
		if match(t) {
			hits = append(hits, hit{taskID: t.ID, path: path})
		}
		return nil
	}); err != nil {
		return 0, err
	}
	for _, h := range hits {
		if err := s.deleteRunsByTaskLocked(h.taskID); err != nil {
			return 0, err
		}
		if err := os.Remove(h.path); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		if err := os.Remove(s.taskRunlogPath(h.taskID)); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	return len(hits), nil
}

func (s *Store) SaveRun(r *task.Run) error {
	if r == nil {
		return fmt.Errorf("run is nil")
	}
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("run id is empty")
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeFile(s.runPath(r.ID), append(data, '\n'))
}

func (s *Store) LoadRun(id string) (*task.Run, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("run id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.runPath(id)
	if !fileExists(path) {
		return nil, nil
	}
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var r task.Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListRunsByTask(taskID string) ([]*task.Run, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*task.Run, 0)
	err := walkDirJSON(s.runsDir, func(path string) error {
		data, err := readFile(path)
		if err != nil {
			return nil
		}
		var r task.Run
		if err := json.Unmarshal(data, &r); err != nil {
			return nil
		}
		if r.TaskID != taskID {
			return nil
		}
		out = append(out, &r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) deleteRunsByTaskLocked(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	return walkDirJSON(s.runsDir, func(path string) error {
		data, err := readFile(path)
		if err != nil {
			return nil
		}
		var r task.Run
		if err := json.Unmarshal(data, &r); err != nil {
			return nil
		}
		if r.TaskID != taskID {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

func (s *Store) taskPath(id string) string {
	return filepath.Join(s.tasksDir, strings.TrimSpace(id)+".json")
}

func (s *Store) runPath(id string) string {
	return filepath.Join(s.runsDir, strings.TrimSpace(id)+".json")
}
