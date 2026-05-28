package desktop

import (
	"context"
	"embed"
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
	srv := newServer(addr, backend, mock)
	return srv.run(ctx)
}

type server struct {
	addr    string
	backend *Backend
	mock    bool
}

func newServer(addr string, b *Backend, mock bool) *server {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:7799"
	}
	return &server{addr: addr, backend: b, mock: mock}
}

func (s *server) run(ctx context.Context) error {
	mux := http.NewServeMux()
	sub, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	api := s.backend.APIHandler(s.mock)
	mux.Handle("/api/", api)

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
	mode := "live"
	if s.mock {
		mode = "mock"
	}
	fmt.Printf("desktop %s listening on http://%s\n", mode, s.addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
