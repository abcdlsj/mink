package session

import (
	"sync"

	"github.com/abcdlsj/mink/bus"
)

type Manager struct {
	store    Store
	sessions map[string]*Session
	sources  map[string]string
	bus      *bus.Bus
	mu       sync.RWMutex
}

func NewManager(store Store, b *bus.Bus) *Manager {
	return &Manager{
		store:    store,
		sessions: make(map[string]*Session),
		sources:  make(map[string]string),
		bus:      b,
	}
}
