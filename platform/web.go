package platform

import (
	"net/http"
	"sync"
)

type Web struct {
	addr      string
	staticDir string
	cb        WebCallbacks
	server    *http.Server
	mu        sync.Mutex

	subMu       sync.Mutex
	nextSubID   int
	subscribers map[int]chan struct{}
}
