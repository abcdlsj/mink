package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/abcdlsj/sumi/gen/go/sumi/system/v1/systemv1connect"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/system"
)

type Server struct {
	handler http.Handler
	store   *store.Store
}

type Config struct {
	DataRoot string
	WebRoot  string
}

func New(ctx context.Context, config Config) (*Server, error) {
	layout, err := home.Ensure(config.DataRoot)
	if err != nil {
		return nil, err
	}
	database, err := store.Open(layout.Database)
	if err != nil {
		return nil, err
	}
	serverID, err := database.ServerID(ctx)
	if err != nil {
		database.Close()
		return nil, err
	}

	mux := http.NewServeMux()
	path, handler := systemv1connect.NewSystemServiceHandler(system.New(serverID))
	mux.Handle(path, handler)
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	if config.WebRoot != "" {
		mux.Handle("/", http.FileServer(http.Dir(config.WebRoot)))
	}

	return &Server{handler: mux, store: database}, nil
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Close() error {
	if err := s.store.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}
