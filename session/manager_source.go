package session

import "github.com/abcdlsj/mink/bus"

func (m *Manager) Current(source string) (*Session, error) {
	if source == "" {
		return m.Create()
	}

	m.mu.RLock()
	id, ok := m.sources[source]
	if ok {
		if s, ok := m.sessions[id]; ok {
			m.mu.RUnlock()
			return s, nil
		}
	}
	m.mu.RUnlock()

	m.mu.Lock()
	id, ok = m.sources[source]
	if ok {
		s, err := m.loadLocked(id)
		m.mu.Unlock()
		return s, err
	}

	s := m.createLocked()
	id = s.ID()
	m.sources[source] = id
	m.mu.Unlock()
	m.publishSessionNew(source, id)
	return s, nil
}

func (m *Manager) LookupSource(source string) (*Session, bool) {
	m.mu.RLock()
	id, ok := m.sources[source]
	if !ok {
		m.mu.RUnlock()
		return nil, false
	}
	if s, ok := m.sessions[id]; ok {
		m.mu.RUnlock()
		return s, true
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	id, ok = m.sources[source]
	if !ok {
		return nil, false
	}
	s, err := m.loadLocked(id)
	if err != nil {
		return nil, false
	}
	return s, true
}

func (m *Manager) RestoreSource(source, id string) error {
	if source == "" || id == "" {
		return nil
	}

	m.mu.Lock()
	if _, err := m.loadLocked(id); err != nil {
		m.mu.Unlock()
		return err
	}
	m.sources[source] = id
	m.mu.Unlock()
	m.publishSessionNew(source, id)
	return nil
}

func (m *Manager) RestoreSources(bindings map[string]string) error {
	for source, id := range bindings {
		if err := m.RestoreSource(source, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ResetSource(source string) (*Session, error) {
	if source == "" {
		return m.Create()
	}

	m.mu.Lock()
	s := m.createLocked()
	id := s.ID()
	m.sources[source] = id
	m.mu.Unlock()

	m.publishSessionNew(source, id)
	return s, nil
}

func (m *Manager) ForkSource(source string) (*Session, error) {
	if source == "" {
		return m.Create()
	}
	parent, err := m.Current(source)
	if err != nil {
		return nil, err
	}
	child, err := m.Fork(parent)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sources[source] = child.ID()
	m.mu.Unlock()
	m.publishSessionNew(source, child.ID())
	return child, nil
}

func (m *Manager) CurrentID(source string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.sources[source]
	return id, ok
}

func (m *Manager) Bindings() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := make(map[string]string, len(m.sources))
	for source, id := range m.sources {
		r[source] = id
	}
	return r
}

func (m *Manager) publishSessionNew(source, id string) {
	if m.bus == nil || source == "" || id == "" {
		return
	}
	_ = m.bus.Pub(bus.Msg{
		Type:    bus.TypeSessionNew,
		From:    bus.AddrSystemSession,
		To:      source,
		Payload: id,
	})
}
