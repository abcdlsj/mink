package desktop

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/app"
)

//go:embed frontend/*
var frontendFS embed.FS

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterEntrypoint("desktop", run)
		return nil
	}
}

func run(ctx context.Context, a *app.App, args []string) error {
	addr := "127.0.0.1:7799"
	mock := false
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	fs.StringVar(&addr, "addr", addr, "desktop bind address")
	fs.BoolVar(&mock, "mock", false, "serve mock data only (skip backend wiring)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var backend *Backend
	if mock {
		backend = newBackend(nil)
	} else {
		backend = newBackend(a)
		backend.start(ctx)
	}
	srv := newServer(addr, backend)
	return srv.run(ctx)
}

type server struct {
	addr    string
	backend *Backend
}

func newServer(addr string, b *Backend) *server {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:7799"
	}
	return &server{addr: addr, backend: b}
}

func (s *server) run(ctx context.Context) error {
	mux := http.NewServeMux()
	sub, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/state", s.json(func() any { return s.backend.WorkspaceInfo() }))
	mux.HandleFunc("/api/sessions", s.json(func() any {
		out, _ := s.backend.ListSessions()
		return out
	}))
	mux.HandleFunc("/api/session", func(rw http.ResponseWriter, req *http.Request) {
		id := req.URL.Query().Get("id")
		out, _ := s.backend.GetSession(id)
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/channels", s.json(func() any { return s.backend.ListChannels() }))
	mux.HandleFunc("/api/threads", s.json(func() any { return s.backend.ListThreads() }))
	mux.HandleFunc("/api/agents", s.json(func() any { return s.backend.ListAgents() }))
	mux.HandleFunc("/api/channel", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, s.backend.GetChannel(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/thread", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, s.backend.GetThread(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/participants", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, s.backend.GetParticipants(req.URL.Query().Get("channel"), req.URL.Query().Get("thread")))
	})
	mux.HandleFunc("/api/personas", s.json(func() any { return s.backend.ListPersonas() }))
	mux.HandleFunc("/api/models", s.json(func() any { return s.backend.ListModels() }))
	mux.HandleFunc("/api/tools", s.json(func() any { return s.backend.ListTools() }))
	mux.HandleFunc("/api/commands", s.json(func() any { return s.backend.ListCommands() }))

	httpSrv := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	fmt.Printf("desktop mock listening on http://%s\n", s.addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *server) json(get func() any) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		writeJSON(rw, get())
	}
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(rw)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
