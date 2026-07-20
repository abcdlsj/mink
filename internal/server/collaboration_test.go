package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/audit/v1/auditv1connect"
	organizationv1 "github.com/abcdlsj/sumi/gen/go/sumi/organization/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/organization/v1/organizationv1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCollaborationRoutePersistsSpaceThreadAndMessagesAcrossRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openCollaborationAPI(t, dataRoot)
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.client.ListSpaces(context.Background(), connect.NewRequest(&spacev1.ListSpacesRequest{})); err == nil {
		t.Fatal("unauthenticated collaboration request succeeded")
	} else {
		assertConnectCode(t, err, connect.CodeUnauthenticated)
	}

	createGroup := connect.NewRequest(&spacev1.CreateGroupRequest{RequestId: uuid.NewString(), Name: "restart-lab"})
	authorize(createGroup, credential)
	groupResponse, err := api.client.CreateGroup(context.Background(), createGroup)
	if err != nil {
		t.Fatal(err)
	}
	group := groupResponse.Msg.GetSpace()
	getOrganization := connect.NewRequest(&organizationv1.GetOrganizationRequest{})
	authorize(getOrganization, credential)
	organizationResponse, err := api.organizations.GetOrganization(context.Background(), getOrganization)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := organizationResponse.Msg.GetOrganization().GetBootstrapHumanId()
	agentResponse, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Name: "audit-context-agent", Driver: agentv1.Driver_DRIVER_NATIVE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentID := agentResponse.Msg.GetAgent().GetId()
	addAgent := connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
	})
	authorize(addAgent, credential)
	if _, err := api.client.AddMember(context.Background(), addAgent); err != nil {
		t.Fatal(err)
	}
	removeAgent := connect.NewRequest(&spacev1.RemoveMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
	})
	authorize(removeAgent, credential)
	if _, err := api.client.RemoveMember(context.Background(), removeAgent); err != nil {
		t.Fatal(err)
	}
	rootRequestID := uuid.NewString()
	sendRoot := connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: rootRequestID,
		Target:    &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: group.GetId()}},
		Body:      "persistent root",
	})
	authorize(sendRoot, credential)
	rootResponse, err := api.client.SendMessage(context.Background(), sendRoot)
	if err != nil {
		t.Fatal(err)
	}
	root := rootResponse.Msg.GetMessage()
	if root.GetRequestId() != rootRequestID || root.GetTargetSequence() != 1 {
		t.Fatalf("root response = %+v", root)
	}
	sendReply := connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(),
		Target:    &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: root.GetId()}},
		Body:      "persistent reply",
	})
	authorize(sendReply, credential)
	replyResponse, err := api.client.SendMessage(context.Background(), sendReply)
	if err != nil {
		t.Fatal(err)
	}
	if replyResponse.Msg.GetMessage().GetThreadRootMessageId() != root.GetId() || replyResponse.Msg.GetMessage().GetTargetSequence() != 1 {
		t.Fatalf("reply response = %+v", replyResponse.Msg.GetMessage())
	}
	archive := connect.NewRequest(&spacev1.ArchiveSpaceRequest{RequestId: uuid.NewString(), SpaceId: group.GetId()})
	authorize(archive, credential)
	if _, err := api.client.ArchiveSpace(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	deniedAgent := connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
	})
	authorize(deniedAgent, credential)
	if _, err := api.client.AddMember(context.Background(), deniedAgent); err == nil {
		t.Fatal("archived agent membership mutation succeeded")
	} else {
		assertConnectCode(t, err, connect.CodeFailedPrecondition)
	}
	listMembers := connect.NewRequest(&spacev1.ListMembersRequest{SpaceId: group.GetId()})
	authorize(listMembers, credential)
	membersResponse, err := api.client.ListMembers(context.Background(), listMembers)
	if err != nil {
		t.Fatal(err)
	}
	if len(membersResponse.Msg.GetMemberships()) != 1 || membersResponse.Msg.GetMemberships()[0].GetPrincipal().GetId() != ownerID {
		t.Fatalf("denied membership changed facts: %+v", membersResponse.Msg.GetMemberships())
	}
	listAudit := connect.NewRequest(&auditv1.ListAuditEventsRequest{Limit: 500})
	authorize(listAudit, credential)
	auditResponse, err := api.audit.ListAuditEvents(context.Background(), listAudit)
	if err != nil {
		t.Fatal(err)
	}
	assertMembershipAuditAPI(t, auditResponse.Msg.GetEvents(), auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_ADD, auditv1.AuditOutcome_AUDIT_OUTCOME_COMMITTED, ownerID, agentID, group.GetId())
	assertMembershipAuditAPI(t, auditResponse.Msg.GetEvents(), auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_REMOVE, auditv1.AuditOutcome_AUDIT_OUTCOME_COMMITTED, ownerID, agentID, group.GetId())
	assertMembershipAuditAPI(t, auditResponse.Msg.GetEvents(), auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_ADD, auditv1.AuditOutcome_AUDIT_OUTCOME_DENIED, ownerID, agentID, group.GetId())
	assertAuditContextAPI(t, auditResponse.Msg.GetEvents(), auditv1.AuditAction_AUDIT_ACTION_MESSAGE_SEND, root.GetId(), auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_SPACE, group.GetId())
	assertAuditContextAPI(t, auditResponse.Msg.GetEvents(), auditv1.AuditAction_AUDIT_ACTION_MESSAGE_SEND, replyResponse.Msg.GetMessage().GetId(), auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_THREAD, root.GetId())
	assertAuditContextAPI(t, auditResponse.Msg.GetEvents(), auditv1.AuditAction_AUDIT_ACTION_THREAD_CREATE, root.GetId(), auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_SPACE, group.GetId())
	encoded, err := protojson.Marshal(replyResponse.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(credential)) || bytes.Contains(encoded, []byte("credential")) {
		t.Fatalf("credential leaked in collaboration response: %s", encoded)
	}
	auditPayload, err := protojson.Marshal(auditResponse.Msg)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{credential, dataRoot, "persistent root", "persistent reply"} {
		if bytes.Contains(auditPayload, []byte(forbidden)) {
			t.Fatalf("free text or secret leaked in audit response: %q in %s", forbidden, auditPayload)
		}
	}
	api.close(t)

	api = openCollaborationAPI(t, dataRoot)
	defer api.close(t)
	getSpace := connect.NewRequest(&spacev1.GetSpaceRequest{SpaceId: group.GetId()})
	authorize(getSpace, credential)
	restartedSpace, err := api.client.GetSpace(context.Background(), getSpace)
	if err != nil || restartedSpace.Msg.GetSpace().GetId() != group.GetId() {
		t.Fatalf("space after restart = %+v, %v", restartedSpace, err)
	}
	listThread := connect.NewRequest(&spacev1.ListMessagesRequest{
		Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: root.GetId()}},
	})
	authorize(listThread, credential)
	threadMessages, err := api.client.ListMessages(context.Background(), listThread)
	if err != nil || len(threadMessages.Msg.GetMessages()) != 1 || threadMessages.Msg.GetMessages()[0].GetBody() != "persistent reply" {
		t.Fatalf("thread after restart = %+v, %v", threadMessages, err)
	}
	databasePayload, err := os.ReadFile(filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databasePayload, []byte(credential)) {
		t.Fatal("raw credential found in collaboration database")
	}
}

type collaborationAPI struct {
	app           *Server
	http          *httptest.Server
	client        spacev1connect.CollaborationServiceClient
	agents        agentv1connect.AgentServiceClient
	audit         auditv1connect.AuditServiceClient
	organizations organizationv1connect.OrganizationServiceClient
}

func openCollaborationAPI(t *testing.T, dataRoot string) collaborationAPI {
	t.Helper()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	return collaborationAPI{
		app: app, http: httpServer,
		client:        spacev1connect.NewCollaborationServiceClient(httpServer.Client(), httpServer.URL),
		agents:        agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL),
		audit:         auditv1connect.NewAuditServiceClient(httpServer.Client(), httpServer.URL),
		organizations: organizationv1connect.NewOrganizationServiceClient(httpServer.Client(), httpServer.URL),
	}
}

func assertMembershipAuditAPI(t *testing.T, events []*auditv1.AuditEvent, action auditv1.AuditAction, outcome auditv1.AuditOutcome, actorID, agentID, spaceID string) {
	t.Helper()
	for _, event := range events {
		if event.GetAction() == action && event.GetOutcome() == outcome && event.GetTargetId() == agentID {
			if event.GetActor().GetId() != actorID || event.GetTargetKind() != auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT || event.GetContextKind() != auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_SPACE || event.GetContextId() != spaceID {
				t.Fatalf("membership audit lost actor/agent/space: %+v", event)
			}
			return
		}
	}
	t.Fatalf("membership audit missing: action=%s outcome=%s agent=%s", action, outcome, agentID)
}

func assertAuditContextAPI(t *testing.T, events []*auditv1.AuditEvent, action auditv1.AuditAction, targetID string, contextKind auditv1.AuditContextKind, contextID string) {
	t.Helper()
	for _, event := range events {
		if event.GetAction() == action && event.GetOutcome() == auditv1.AuditOutcome_AUDIT_OUTCOME_COMMITTED && event.GetTargetId() == targetID {
			if event.GetContextKind() != contextKind || event.GetContextId() != contextID {
				t.Fatalf("audit context mismatch: %+v", event)
			}
			return
		}
	}
	t.Fatalf("audit context event missing: action=%s target=%s", action, targetID)
}

func (api collaborationAPI) close(t *testing.T) {
	t.Helper()
	api.http.Close()
	if err := api.app.Close(); err != nil {
		t.Fatal(err)
	}
}
