package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/system/v1/systemv1connect"
	"github.com/abcdlsj/sumi/internal/agent"
	"github.com/abcdlsj/sumi/internal/computer"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/placement"
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
	systemPath, systemHandler := systemv1connect.NewSystemServiceHandler(system.New(serverID))
	mux.Handle(systemPath, systemHandler)
	computerPath, computerHandler := computerv1connect.NewComputerServiceHandler(computer.New(database))
	mux.Handle(computerPath, computerHandler)
	agentPath, agentHandler := agentv1connect.NewAgentServiceHandler(agent.New(database))
	mux.Handle(agentPath, agentHandler)
	placementPath, placementHandler := placementv1connect.NewPlacementServiceHandler(placement.New(database))
	mux.Handle(placementPath, placementHandler)
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
