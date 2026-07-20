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
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
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
	"github.com/abcdlsj/sumi/internal/runtimeauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/system"
	"github.com/abcdlsj/sumi/internal/websession"
)

type Server struct {
	handler http.Handler
	store   *store.Store
}

type Config struct {
	DataRoot                string
	WebRoot                 string
	BootstrapCredentialFile string
	BrowserOrigin           string
}

func New(ctx context.Context, config Config) (*Server, error) {
	if err := authority.ValidateBrowserOrigin(config.BrowserOrigin); err != nil {
		return nil, err
	}
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
	authorization := connect.WithInterceptors(authority.NewBrowserInterceptor(database, database, authority.BrowserInterceptorConfig{
		Origin: config.BrowserOrigin, BrowserReadProcedures: humanReadProcedures(),
	}))
	systemPath, systemHandler := systemv1connect.NewSystemServiceHandler(system.New(serverID))
	mux.Handle(systemPath, systemHandler)
	computerPath, computerHandler := computerv1connect.NewComputerServiceHandler(computer.New(database))
	mux.Handle(computerPath, computerHandler)
	agentMutationAuthorization := connect.WithInterceptors(authority.NewBrowserInterceptor(database, database, authority.BrowserInterceptorConfig{
		Origin:              config.BrowserOrigin,
		ProtectedProcedures: []string{agentv1connect.AgentServiceCreateAgentProcedure},
	}))
	agentPath, agentHandler := agentv1connect.NewAgentServiceHandler(agent.New(database), agentMutationAuthorization)
	mux.Handle(agentPath, agentHandler)
	placementMutationAuthorization := connect.WithInterceptors(authority.NewBrowserInterceptor(database, database, authority.BrowserInterceptorConfig{
		Origin:              config.BrowserOrigin,
		ProtectedProcedures: []string{placementv1connect.PlacementServiceSetAgentPlacementProcedure},
	}))
	placementPath, placementHandler := placementv1connect.NewPlacementServiceHandler(placement.New(database), placementMutationAuthorization)
	mux.Handle(placementPath, placementHandler)
	agentRuntimeAuthorization := connect.WithInterceptors(runtimeauth.NewProcedureInterceptor(
		database,
		runtimev1connect.AgentRuntimeServiceRenewAgentRuntimeSessionProcedure,
		runtimev1connect.AgentRuntimeServiceRevokeAgentRuntimeSessionProcedure,
	))
	agentRuntimePath, agentRuntimeHandler := runtimev1connect.NewAgentRuntimeServiceHandler(
		runtimeauth.NewService(database, runtimeauth.Config{}), agentRuntimeAuthorization,
	)
	mux.Handle(agentRuntimePath, agentRuntimeHandler)
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
	if config.BrowserOrigin != "" {
		browserSessions, err := websession.New(database, websession.Config{Origin: config.BrowserOrigin})
		if err != nil {
			database.Close()
			return nil, err
		}
		mux.Handle("/auth/", browserSessions)
	}
	if config.WebRoot != "" {
		mux.Handle("/", http.FileServer(http.Dir(config.WebRoot)))
	}
	handler, err := authority.BrowserRequestMiddleware(config.BrowserOrigin, mux)
	if err != nil {
		database.Close()
		return nil, err
	}

	return &Server{handler: handler, store: database}, nil
}

func humanReadProcedures() []string {
	return []string{
		organizationv1connect.OrganizationServiceGetOrganizationProcedure,
		organizationv1connect.OrganizationServiceGetHumanProcedure,
		organizationv1connect.OrganizationServiceListHumansProcedure,
		grantv1connect.GrantServiceGetGrantProcedure,
		grantv1connect.GrantServiceListGrantsProcedure,
		grantv1connect.GrantServiceCheckPermissionProcedure,
		auditv1connect.AuditServiceListAuditEventsProcedure,
		spacev1connect.CollaborationServiceGetSpaceProcedure,
		spacev1connect.CollaborationServiceListSpacesProcedure,
		spacev1connect.CollaborationServiceListMembersProcedure,
		spacev1connect.CollaborationServiceGetMessageProcedure,
		spacev1connect.CollaborationServiceGetThreadProcedure,
		spacev1connect.CollaborationServiceListMessagesProcedure,
	}
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
