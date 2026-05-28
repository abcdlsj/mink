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

//go:embed all:frontend/dist
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
	sub, err := fs.Sub(frontendFS, "frontend/dist")
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
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/send", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in SendRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		out, err := s.backend.SendMessage(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, map[string]string{"reply": out})
	})
	mux.HandleFunc("/api/stop", func(rw http.ResponseWriter, req *http.Request) {
		var in struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&in)
		if in.SessionID == "" {
			in.SessionID = req.URL.Query().Get("session")
		}
		_ = s.backend.StopTurn(in.SessionID)
		writeJSON(rw, map[string]bool{"ok": true})
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

func (s *server) handleEvents(rw http.ResponseWriter, req *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	events, cancel := s.backend.Subscribe()
	defer cancel()
	flusher.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(rw, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(rw, "event: bus\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(rw)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
