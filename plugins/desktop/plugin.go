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
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	fs.StringVar(&addr, "addr", addr, "desktop bind address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	backend := newBackend(a)
	backend.start(ctx)
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
	mux.Handle("/api/", s.backend.APIHandler())

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
	fmt.Printf("desktop listening on http://%s\n", s.addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
