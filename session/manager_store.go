package session

import "sort"

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	if s, ok := m.sessions[id]; ok {
		m.mu.RUnlock()
		return s, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked(id)
}

func (m *Manager) List() ([]string, error) {
	persisted, err := m.store.List()
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	seen := make(map[string]struct{}, len(persisted)+len(m.sessions))
	ids := make([]string, 0, len(persisted)+len(m.sessions))
	for _, id := range persisted {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range m.sessions {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	sort.Strings(ids)
	return ids, nil
}

func (m *Manager) FlushAll() error {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.RUnlock()

	for _, s := range sessions {
		if err := s.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	delete(m.sessions, id)
	for source, sid := range m.sources {
		if sid == id {
			delete(m.sources, source)
		}
	}
	m.mu.Unlock()
	return m.store.Delete(id)
}

func (m *Manager) Update(id string, fn func(*Session)) error {
	if fn == nil || id == "" {
		return nil
	}
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	fn(s)
	return nil
}

func (m *Manager) UpdateSource(source string, fn func(*Session)) error {
	if fn == nil || source == "" {
		return nil
	}
	s, err := m.Current(source)
	if err != nil {
		return err
	}
	fn(s)
	return nil
}
