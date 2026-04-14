package webapp

import (
	"net/http"
	"sync"

	platformshared "github.com/abcdlsj/mink/platform"
)

type Web struct {
	addr      string
	staticDir string
	cb        platformshared.WebCallbacks
	server    *http.Server
	mu        sync.Mutex

	subMu       sync.Mutex
	nextSubID   int
	subscribers map[int]chan struct{}
}
