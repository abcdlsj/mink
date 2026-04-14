package webapp

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	platformshared "github.com/abcdlsj/mink/platform"
)

func NewWeb(addr string, cb platformshared.WebCallbacks) *Web {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:7788"
	}
	return &Web{
		addr:        addr,
		cb:          cb,
		subscribers: make(map[int]chan struct{}),
	}
}

func (w *Web) SetStaticDir(dir string) { w.staticDir = dir }

func (w *Web) ID() string { return "web" }

func (w *Web) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.server != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", w.handleState)
	mux.HandleFunc("/api/events", w.handleEvents)
	mux.HandleFunc("/api/select", w.handleSelect)
	mux.HandleFunc("/api/message", w.handleMessage)
	mux.HandleFunc("/api/action", w.handleAction)

	if w.staticDir != "" {
		staticFS := os.DirFS(w.staticDir)
		fileServer := http.FileServer(http.FS(staticFS))
		mux.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
			path := strings.TrimPrefix(req.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticFS, path); err != nil {
				path = "index.html"
			}
			req.URL.Path = "/" + path
			fileServer.ServeHTTP(rw, req)
		})
	} else {
		mux.HandleFunc("/", w.handleIndex)
	}

	srv := &http.Server{
		Addr:              w.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	w.server = srv

	go func() {
		<-ctx.Done()
		_ = w.Stop()
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("web server error: %v\n", err)
		}
	}()

	fmt.Printf("Web UI listening on http://%s\n", w.addr)
	return nil
}

func (w *Web) Stop() error {
	w.mu.Lock()
	srv := w.server
	w.server = nil
	w.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func (w *Web) NotifyStateChanged() {
	w.subMu.Lock()
	subs := make([]chan struct{}, 0, len(w.subscribers))
	for _, ch := range w.subscribers {
		subs = append(subs, ch)
	}
	w.subMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (w *Web) subscribe() (int, chan struct{}) {
	w.subMu.Lock()
	defer w.subMu.Unlock()
	id := w.nextSubID
	w.nextSubID++
	ch := make(chan struct{}, 1)
	w.subscribers[id] = ch
	return id, ch
}

func (w *Web) unsubscribe(id int) {
	w.subMu.Lock()
	ch, ok := w.subscribers[id]
	if ok {
		delete(w.subscribers, id)
		close(ch)
	}
	w.subMu.Unlock()
}
