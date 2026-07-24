package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1/artifactv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/audit/v1/auditv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1/knowledgev1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/organization/v1/organizationv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/system/v1/systemv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/work/v1/workv1connect"
	"github.com/abcdlsj/sumi/internal/agent"
	"github.com/abcdlsj/sumi/internal/artifact"
	artifactblob "github.com/abcdlsj/sumi/internal/artifact/blob"
	"github.com/abcdlsj/sumi/internal/audit"
	"github.com/abcdlsj/sumi/internal/authority"
	runtimeauth "github.com/abcdlsj/sumi/internal/authority/runtime"
	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/collaboration"
	"github.com/abcdlsj/sumi/internal/computer"
	"github.com/abcdlsj/sumi/internal/execution/inbox"
	executionrun "github.com/abcdlsj/sumi/internal/execution/run"
	"github.com/abcdlsj/sumi/internal/grant"
	"github.com/abcdlsj/sumi/internal/home"
	knowledgeindex "github.com/abcdlsj/sumi/internal/knowledge"
	"github.com/abcdlsj/sumi/internal/observability"
	"github.com/abcdlsj/sumi/internal/organization"
	"github.com/abcdlsj/sumi/internal/placement"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/system"
	"github.com/abcdlsj/sumi/internal/work"
	"github.com/abcdlsj/sumi/internal/workattention"
)

type Server struct {
	handler   http.Handler
	store     *store.Store
	knowledge *knowledgeindex.Reconciler
}

type Config struct {
	DataRoot                string
	WebRoot                 string
	BootstrapCredentialFile string
	BrowserOrigin           string
	Logger                  *observability.Logger
}

func New(ctx context.Context, config Config) (*Server, error) {
	lifecycleLogger := observability.CategoryLogger(config.Logger, observability.ComponentServer, observability.CategoryLifecycle)
	authorityLogger := observability.CategoryLogger(config.Logger, observability.ComponentServer, observability.CategoryAuthority)
	artifactLogger := observability.CategoryLogger(config.Logger, observability.ComponentServer, observability.CategoryArtifact)
	knowledgeLogger := observability.CategoryLogger(config.Logger, observability.ComponentServer, observability.CategoryKnowledge)
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
	lifecycleLogger.Info("server database opened", "event", "server.database.opened")
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
	authorityLogger.Info("bootstrap authority ready", "event", "authority.bootstrap.ready", "server_id", serverID, "existing", authorityExists)
	blobs, err := artifactblob.OpenLocal(layout.Artifacts)
	if err != nil {
		database.Close()
		return nil, err
	}
	artifacts, err := store.NewArtifactStore(database, blobs, store.ArtifactMaxBlobSize)
	if err != nil {
		database.Close()
		return nil, err
	}
	reconcileResult, err := artifacts.Reconcile(ctx, time.Now())
	if err != nil {
		database.Close()
		return nil, err
	}
	artifactLogger.Info("artifact inventory reconciled", "event", "artifact.inventory.reconciled", "ready", reconcileResult.Ready, "missing", reconcileResult.Missing, "corrupt", reconcileResult.Corrupt, "quarantined", reconcileResult.Quarantined, "deleted", reconcileResult.Deleted)

	mux := http.NewServeMux()
	authorization := connect.WithInterceptors(authority.NewBrowserInterceptor(database, database, authority.BrowserInterceptorConfig{
		Origin: config.BrowserOrigin, BrowserReadProcedures: humanReadProcedures(),
	}))
	systemPath, systemHandler := systemv1connect.NewSystemServiceHandler(system.New(serverID))
	mux.Handle(systemPath, systemHandler)
	computerMutationAuthorization := connect.WithInterceptors(authority.NewBrowserInterceptor(database, database, authority.BrowserInterceptorConfig{
		Origin: config.BrowserOrigin,
		ProtectedProcedures: []string{
			computerv1connect.ComputerServiceCreateComputerPairingProcedure,
			computerv1connect.ComputerServiceEnqueueCredentialDeliveryProcedure,
			computerv1connect.ComputerServiceListCredentialDeliveriesProcedure,
		},
		BrowserReadProcedures: []string{computerv1connect.ComputerServiceListCredentialDeliveriesProcedure},
	}))
	computerPath, computerHandler := computerv1connect.NewComputerServiceHandler(computer.New(database), computerMutationAuthorization)
	mux.Handle(computerPath, computerHandler)
	agentMutationAuthorization := connect.WithInterceptors(authority.NewBrowserInterceptor(database, database, authority.BrowserInterceptorConfig{
		Origin: config.BrowserOrigin,
		ProtectedProcedures: []string{
			agentv1connect.AgentServiceCreateAgentProcedure,
			agentv1connect.AgentServiceUpdateAgentProfileProcedure,
			agentv1connect.AgentServiceUpdateAgentRuntimeSpecProcedure,
		},
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
	inboxPath, inboxHandler := inboxv1connect.NewInboxServiceHandler(inbox.New(database, config.BrowserOrigin))
	mux.Handle(inboxPath, inboxHandler)
	workAttentionPath, workAttentionHandler := inboxv1connect.NewWorkAttentionServiceHandler(workattention.New(database, config.BrowserOrigin))
	mux.Handle(workAttentionPath, workAttentionHandler)
	runAuthorization := connect.WithInterceptors(runtimeauth.NewProcedureInterceptor(database, executionrun.Procedures()...))
	runPath, runHandler := runv1connect.NewRunServiceHandler(executionrun.New(database), runAuthorization)
	mux.Handle(runPath, runHandler)
	knowledgePath, knowledgeHandler := knowledgev1connect.NewKnowledgeServiceHandler(newKnowledgeService(database, config.BrowserOrigin))
	mux.Handle(knowledgePath, knowledgeHandler)
	artifactPath, artifactHandler := artifactv1connect.NewArtifactServiceHandler(artifact.New(artifacts, database, config.BrowserOrigin))
	mux.Handle(artifactPath, artifactHandler)
	workPath, workHandler := workv1connect.NewWorkServiceHandler(work.New(database, config.BrowserOrigin))
	mux.Handle(workPath, workHandler)
	organizationPath, organizationHandler := organizationv1connect.NewOrganizationServiceHandler(organization.New(database), authorization)
	mux.Handle(organizationPath, organizationHandler)
	grantPath, grantHandler := grantv1connect.NewGrantServiceHandler(grant.New(database), authorization)
	mux.Handle(grantPath, grantHandler)
	auditPath, auditHandler := auditv1connect.NewAuditServiceHandler(audit.New(database), authorization)
	mux.Handle(auditPath, auditHandler)
	collaborationAuthorization := connect.WithInterceptors(authority.NewBrowserInterceptor(database, database, authority.BrowserInterceptorConfig{
		Origin:                config.BrowserOrigin,
		ProtectedProcedures:   collaborationBrowserProcedures(),
		BrowserReadProcedures: collaborationBrowserReadProcedures(),
	}))
	collaborationPath, collaborationHandler := spacev1connect.NewCollaborationServiceHandler(
		collaboration.New(database, config.BrowserOrigin), collaborationAuthorization,
	)
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

	handler = observability.HTTPMiddleware(config.Logger, handler)
	runner := knowledgeindex.NewWithLogger(database, config.Logger)
	runner.Start(ctx)
	knowledgeLogger.Info("knowledge reconciler started", "event", "knowledge.reconciler.started")
	lifecycleLogger.Info("server initialized", "event", "server.initialized", "server_id", serverID, "browser_enabled", config.BrowserOrigin != "", "web_enabled", config.WebRoot != "")
	return &Server{handler: handler, store: database, knowledge: runner}, nil
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
	}
}

func collaborationBrowserProcedures() []string {
	return []string{
		spacev1connect.CollaborationServiceCreateDMProcedure,
		spacev1connect.CollaborationServiceCreateGroupProcedure,
		spacev1connect.CollaborationServiceGetSpaceProcedure,
		spacev1connect.CollaborationServiceListSpacesProcedure,
		spacev1connect.CollaborationServiceAddMemberProcedure,
		spacev1connect.CollaborationServiceRemoveMemberProcedure,
		spacev1connect.CollaborationServiceListMembersProcedure,
		spacev1connect.CollaborationServiceArchiveSpaceProcedure,
		spacev1connect.CollaborationServiceUnarchiveSpaceProcedure,
	}
}

func collaborationBrowserReadProcedures() []string {
	return []string{
		spacev1connect.CollaborationServiceGetSpaceProcedure,
		spacev1connect.CollaborationServiceListSpacesProcedure,
		spacev1connect.CollaborationServiceListMembersProcedure,
	}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Close() error {
	s.knowledge.Close()
	if err := s.store.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}
