package server

import (
	"context"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/audit/v1/auditv1connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestAgentMutationsRequireHumanAuthorityWithoutProtectingComputerRPCs(t *testing.T) {
	dataRoot := t.TempDir()
	ownerCmd, sessionToken := registerTestOwner(t)
	api := openAgentAuthorityAPI(t, dataRoot, sessionToken)
	now := time.Now()
	ownerCmd.Now = now
	ownerCmd.SessionExpiresAt = now.Add(12 * time.Hour)
	bootstrap, err := api.app.store.RegisterFirstOwner(context.Background(), ownerCmd)
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	peerDigest, hashErr := localauth.HashPassword(rand.Reader, "agent-api-peer-password-1234567890", localauth.DefaultPasswordParameters())
	if hashErr != nil {
		api.close(t)
		t.Fatal(hashErr)
	}
	peerHuman, err := api.app.store.CreateHuman(context.Background(), store.CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Agent API Peer", Role: "member",
		Identity: authorityapp.AuthenticationIdentity{Provider: "local", Subject: "agentapipeer"},
		Password: peerDigest, Now: now.Add(time.Second),
	})
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	peer := store.Principal{Kind: "human", ID: peerHuman.ID, OrganizationID: owner.OrganizationID}
	peerAgents := agentv1connect.NewAgentServiceClient(api.http.Client(), api.http.URL, browserSessionAuth(sessionToken, ""))
	peerPlacements := placementv1connect.NewPlacementServiceClient(api.http.Client(), api.http.URL, browserSessionAuth(sessionToken, ""))

	if _, err := api.rawAgents.ListAgents(context.Background(), connect.NewRequest(&agentv1.ListAgentsRequest{})); err != nil {
		api.close(t)
		t.Fatalf("public agent read: %v", err)
	}
	unauthenticatedCreate := agentRequest("unauthenticated-agent")
	if _, err := api.rawAgents.CreateAgent(context.Background(), connect.NewRequest(unauthenticatedCreate)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		api.close(t)
		t.Fatalf("unauthenticated create error = %v", err)
	}
	delegatedCreate := agentRequest("delegated-api-agent")
	if _, err := peerAgents.CreateAgent(context.Background(), connect.NewRequest(delegatedCreate)); connect.CodeOf(err) != connect.CodePermissionDenied {
		api.close(t)
		t.Fatalf("create without grant error = %v", err)
	}
	if listed, err := api.rawAgents.ListAgents(context.Background(), connect.NewRequest(&agentv1.ListAgentsRequest{})); err != nil || len(listed.Msg.GetAgents()) != 0 {
		api.close(t)
		t.Fatalf("denied create agents = %+v, %v", listed, err)
	}
	createGrant := issueServerGrant(t, api.app.store, owner, peer, store.CapabilityAgentCreate, store.Scope{Kind: "organization", ID: owner.OrganizationID}, bootstrap.RootGrant.ID, now.Add(2*time.Second))
	delegatedAgentResponse, err := peerAgents.CreateAgent(context.Background(), connect.NewRequest(delegatedCreate))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	delegatedAgent := delegatedAgentResponse.Msg.GetAgent()
	if _, err := api.ownerAgents.CreateAgent(context.Background(), connect.NewRequest(delegatedCreate)); connect.CodeOf(err) != connect.CodeAlreadyExists {
		api.close(t)
		t.Fatalf("cross-actor create replay error = %v", err)
	}
	if _, err := api.app.store.RevokeGrant(context.Background(), store.RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: owner, GrantID: createGrant.ID, Now: now.Add(3 * time.Second),
	}); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if _, err := peerAgents.CreateAgent(context.Background(), connect.NewRequest(delegatedCreate)); connect.CodeOf(err) != connect.CodePermissionDenied {
		api.close(t)
		t.Fatalf("revoked create replay error = %v", err)
	}

	computerKey := "agent-api-computer-key-abcdefghijklmnopqrstuvwxyz"
	computerResponse := pairComputerClients(t, api.ownerComputers, api.computers, computerKey, "Agent API Computer", computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, computerv1.Architecture_ARCHITECTURE_AMD64)
	computerID := computerResponse.Msg.GetComputer().GetId()
	placementRequest := &placementv1.SetAgentPlacementRequest{RequestId: uuid.NewString(), AgentId: delegatedAgent.GetId(), ComputerId: computerID}
	if _, err := api.rawPlacements.SetAgentPlacement(context.Background(), connect.NewRequest(placementRequest)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		api.close(t)
		t.Fatalf("unauthenticated placement error = %v", err)
	}
	if _, err := peerPlacements.SetAgentPlacement(context.Background(), connect.NewRequest(placementRequest)); connect.CodeOf(err) != connect.CodePermissionDenied {
		api.close(t)
		t.Fatalf("placement without grant error = %v", err)
	}
	if _, err := api.rawPlacements.GetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.GetAgentPlacementRequest{AgentId: delegatedAgent.GetId()})); connect.CodeOf(err) != connect.CodeNotFound {
		api.close(t)
		t.Fatalf("denied placement fact error = %v", err)
	}
	placeGrant := issueServerGrant(t, api.app.store, owner, peer, store.CapabilityAgentPlace, store.Scope{Kind: "agent", ID: delegatedAgent.GetId()}, bootstrap.RootGrant.ID, now.Add(4*time.Second))
	prepareServerTestPlacement(t, api.ownerComputers, api.computers, api.ownerAgents, delegatedAgent.GetId(), computerID, computerKey)
	_, err = peerPlacements.SetAgentPlacement(context.Background(), connect.NewRequest(placementRequest))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if _, err := api.app.store.RevokeGrant(context.Background(), store.RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: owner, GrantID: placeGrant.ID, Now: now.Add(5 * time.Second),
	}); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if _, err := peerPlacements.SetAgentPlacement(context.Background(), connect.NewRequest(placementRequest)); connect.CodeOf(err) != connect.CodePermissionDenied {
		api.close(t)
		t.Fatalf("revoked placement replay error = %v", err)
	}
	if _, err := api.ownerPlacements.SetAgentPlacement(context.Background(), connect.NewRequest(placementRequest)); connect.CodeOf(err) != connect.CodeAlreadyExists {
		api.close(t)
		t.Fatalf("cross-actor placement replay error = %v", err)
	}

	restartCreate := agentRequest("restart-api-agent")
	restartAgentResponse, err := api.ownerAgents.CreateAgent(context.Background(), connect.NewRequest(restartCreate))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	prepareServerTestPlacement(t, api.ownerComputers, api.computers, api.ownerAgents, restartAgentResponse.Msg.GetAgent().GetId(), computerID, computerKey)
	restartPlacement := &placementv1.SetAgentPlacementRequest{RequestId: uuid.NewString(), AgentId: restartAgentResponse.Msg.GetAgent().GetId(), ComputerId: computerID}
	restartPlacementResponse, err := api.ownerPlacements.SetAgentPlacement(context.Background(), connect.NewRequest(restartPlacement))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	assignments, err := api.rawPlacements.ListComputerAssignments(context.Background(), connect.NewRequest(&placementv1.ListComputerAssignmentsRequest{
		ComputerId: computerID, RegistrationKey: computerKey,
	}))
	if err != nil || len(assignments.Msg.GetAssignments()) != 2 {
		api.close(t)
		t.Fatalf("computer assignments = %+v, %v", assignments, err)
	}
	if _, err := api.rawPlacements.AcknowledgeAgentPlacement(context.Background(), connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId: computerID, RegistrationKey: computerKey, AgentId: restartAgentResponse.Msg.GetAgent().GetId(),
		DesiredRevision: restartPlacementResponse.Msg.GetPlacement().GetDesiredRevision(), Result: placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_READY,
	})); err != nil {
		api.close(t)
		t.Fatalf("computer acknowledgement: %v", err)
	}

	if _, err := api.app.store.SetHumanStatus(context.Background(), store.SetHumanStatusParams{
		RequestID: uuid.NewString(), Actor: owner, HumanID: peer.ID, Status: "disabled", Now: now.Add(6 * time.Second),
	}); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if _, err := peerAgents.CreateAgent(context.Background(), connect.NewRequest(agentRequest("disabled-api-agent"))); connect.CodeOf(err) != connect.CodeUnauthenticated {
		api.close(t)
		t.Fatalf("disabled credential error = %v", err)
	}

	audits, err := api.ownerAudits.ListAuditEvents(context.Background(), connect.NewRequest(&auditv1.ListAuditEventsRequest{Limit: 500}))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	assertAgentAuthorityAuditAPI(t, audits.Msg.GetEvents(), delegatedCreate.GetRequestId(), auditv1.AuditAction_AUDIT_ACTION_AGENT_CREATE, auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT, delegatedAgent.GetId(), auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_UNSPECIFIED, "", auditv1.AuditOutcome_AUDIT_OUTCOME_COMMITTED)
	assertAgentAuthorityAuditAPI(t, audits.Msg.GetEvents(), delegatedCreate.GetRequestId(), auditv1.AuditAction_AUDIT_ACTION_AGENT_CREATE, auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT, "", auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_UNSPECIFIED, "", auditv1.AuditOutcome_AUDIT_OUTCOME_DENIED)
	assertAgentAuthorityAuditAPI(t, audits.Msg.GetEvents(), placementRequest.GetRequestId(), auditv1.AuditAction_AUDIT_ACTION_AGENT_PLACE, auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT, delegatedAgent.GetId(), auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_COMPUTER, computerID, auditv1.AuditOutcome_AUDIT_OUTCOME_COMMITTED)
	assertAgentAuthorityAuditAPI(t, audits.Msg.GetEvents(), placementRequest.GetRequestId(), auditv1.AuditAction_AUDIT_ACTION_AGENT_PLACE, auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT, delegatedAgent.GetId(), auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_COMPUTER, computerID, auditv1.AuditOutcome_AUDIT_OUTCOME_DENIED)
	encoded, err := protojson.Marshal(audits.Msg)
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	for _, secret := range []string{computerKey, dataRoot} {
		if strings.Contains(string(encoded), secret) {
			api.close(t)
			t.Fatalf("audit response leaked %q", secret)
		}
	}

	api.close(t)
	api = openAgentAuthorityAPI(t, dataRoot, sessionToken)
	defer api.close(t)
	replayedAgent, err := api.ownerAgents.CreateAgent(context.Background(), connect.NewRequest(restartCreate))
	if err != nil || !proto.Equal(replayedAgent.Msg.GetAgent(), restartAgentResponse.Msg.GetAgent()) {
		t.Fatalf("restart agent replay = %+v, %v", replayedAgent, err)
	}
	replayedPlacement, err := api.ownerPlacements.SetAgentPlacement(context.Background(), connect.NewRequest(restartPlacement))
	if err != nil || !proto.Equal(replayedPlacement.Msg.GetPlacement(), restartPlacementResponse.Msg.GetPlacement()) {
		t.Fatalf("restart placement replay = %+v, %v", replayedPlacement, err)
	}
	currentPlacement, err := api.rawPlacements.GetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.GetAgentPlacementRequest{AgentId: restartAgentResponse.Msg.GetAgent().GetId()}))
	if err != nil || currentPlacement.Msg.GetPlacement().GetState() != placementv1.PlacementState_PLACEMENT_STATE_READY {
		t.Fatalf("current placement after receipt replay = %+v, %v", currentPlacement, err)
	}
}

type agentAuthorityAPI struct {
	app             *Server
	http            *httptest.Server
	rawAgents       agentv1connect.AgentServiceClient
	ownerAgents     agentv1connect.AgentServiceClient
	computers       computerv1connect.ComputerServiceClient
	ownerComputers  computerv1connect.ComputerServiceClient
	rawPlacements   placementv1connect.PlacementServiceClient
	ownerPlacements placementv1connect.PlacementServiceClient
	ownerAudits     auditv1connect.AuditServiceClient
}

func openAgentAuthorityAPI(t *testing.T, dataRoot string, sessionToken string) *agentAuthorityAPI {
	t.Helper()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	authorization := browserSessionAuth(sessionToken, "")
	return &agentAuthorityAPI{
		app:       app,
		http:      httpServer,
		rawAgents: agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL),
		ownerAgents: agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL, authorization),
		computers: computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL),
		ownerComputers: computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL, authorization),
		rawPlacements: placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL),
		ownerPlacements: placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL, authorization),
		ownerAudits: auditv1connect.NewAuditServiceClient(httpServer.Client(), httpServer.URL, authorization),
	}
}

func (api *agentAuthorityAPI) close(t *testing.T) {
	t.Helper()
	api.http.Close()
	if err := api.app.Close(); err != nil {
		t.Fatal(err)
	}
}

func issueServerGrant(t *testing.T, database *store.Store, owner, subject store.Principal, capability store.Capability, scope store.Scope, parentID string, now time.Time) store.Grant {
	t.Helper()
	grant, err := database.IssueGrant(context.Background(), store.IssueGrantParams{
		RequestID: uuid.NewString(), Actor: owner, Subject: subject, Capability: capability,
		Scope: scope, ParentGrantID: parentID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func assertAgentAuthorityAuditAPI(t *testing.T, events []*auditv1.AuditEvent, requestID string, action auditv1.AuditAction, targetKind auditv1.AuditTargetKind, targetID string, contextKind auditv1.AuditContextKind, contextID string, outcome auditv1.AuditOutcome) {
	t.Helper()
	for _, event := range events {
		if event.GetRequestId() == requestID && event.GetAction() == action && event.GetTargetKind() == targetKind &&
			event.GetTargetId() == targetID && event.GetContextKind() == contextKind && event.GetContextId() == contextID && event.GetOutcome() == outcome {
			return
		}
	}
	t.Fatalf("audit event not found: request=%q action=%v target=%v/%q context=%v/%q outcome=%v", requestID, action, targetKind, targetID, contextKind, contextID, outcome)
}
