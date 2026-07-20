package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/audit/v1/auditv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/organization/v1/organizationv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/system/v1/systemv1connect"
	"github.com/abcdlsj/sumi/internal/agent"
	"github.com/abcdlsj/sumi/internal/audit"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/collaboration"
	"github.com/abcdlsj/sumi/internal/computer"
	"github.com/abcdlsj/sumi/internal/grant"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/organization"
	"github.com/abcdlsj/sumi/internal/placement"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/system"
)

type Server struct {
	handler http.Handler
	store   *store.Store
}

type Config struct {
	DataRoot                string
	WebRoot                 string
	BootstrapCredentialFile string
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
	authorityExists, err := database.AuthorityExists(ctx)
	if err != nil {
		database.Close()
		return nil, err
	}
	credentialPath := config.BootstrapCredentialFile
	if credentialPath == "" {
		credentialPath = layout.BootstrapCredential
	}
	credential, err := authority.EnsureBootstrapCredential(credentialPath, authorityExists)
	if err != nil {
		database.Close()
		return nil, err
	}
	if _, err := database.EnsureAuthority(ctx, credential, time.Now()); err != nil {
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
	authorization := connect.WithInterceptors(authority.NewInterceptor(database))
	organizationPath, organizationHandler := organizationv1connect.NewOrganizationServiceHandler(organization.New(database), authorization)
	mux.Handle(organizationPath, organizationHandler)
	grantPath, grantHandler := grantv1connect.NewGrantServiceHandler(grant.New(database), authorization)
	mux.Handle(grantPath, grantHandler)
	auditPath, auditHandler := auditv1connect.NewAuditServiceHandler(audit.New(database), authorization)
	mux.Handle(auditPath, auditHandler)
	collaborationPath, collaborationHandler := spacev1connect.NewCollaborationServiceHandler(collaboration.New(database), authorization)
	mux.Handle(collaborationPath, collaborationHandler)
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
