package collaboration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCollaborationConnectAuthenticationLifecycleAndNoCredentialLeak(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	credential := "collaboration-http-credential-abcdefghijklmnopqrstuvwxyz"
	now := time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), credential, now)
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	peer, err := database.CreateHuman(context.Background(), store.CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "HTTP Peer", Role: "member",
		Credential: "collaboration-peer-http-credential-abcdefghijklmnop", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	service := New(database)
	service.now = func() time.Time { return now.Add(time.Minute) }
	path, handler := spacev1connect.NewCollaborationServiceHandler(service, connect.WithInterceptors(authority.NewInterceptor(database)))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := spacev1connect.NewCollaborationServiceClient(server.Client(), server.URL)

	unauthenticated := connect.NewRequest(&spacev1.CreateGroupRequest{RequestId: uuid.NewString(), Name: "unauthenticated"})
	if _, err := client.CreateGroup(context.Background(), unauthenticated); connectCode(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated create code = %v, error = %v", connectCode(err), err)
	}

	groupRequestID := uuid.NewString()
	groupResponse, err := client.CreateGroup(context.Background(), authenticatedRequest(credential, &spacev1.CreateGroupRequest{
		RequestId: groupRequestID, Name: "connect-lab",
	}))
	if err != nil {
		t.Fatal(err)
	}
	group := groupResponse.Msg.GetSpace()
	if group.GetId() == "" || group.GetOrganizationId() != bootstrap.Organization.ID || group.GetKind() != spacev1.SpaceKind_SPACE_KIND_GROUP {
		t.Fatalf("group response = %+v", group)
	}
	if _, err := client.CreateGroup(context.Background(), authenticatedRequest(credential, &spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: " surrounding ",
	})); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid group name code = %v, error = %v", connectCode(err), err)
	}
	agent, err := database.CreateAgent(context.Background(), store.CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "collaboration-mention", Driver: "native", Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AddMember(context.Background(), authenticatedRequest(credential, &spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.ID},
	})); err != nil {
		t.Fatal(err)
	}

	dmResponse, err := client.CreateDM(context.Background(), authenticatedRequest(credential, &spacev1.CreateDMRequest{
		RequestId: uuid.NewString(), Peer: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: peer.ID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if dmResponse.Msg.GetSpace().GetKind() != spacev1.SpaceKind_SPACE_KIND_DM {
		t.Fatalf("dm response = %+v", dmResponse.Msg.GetSpace())
	}
	if _, err := client.CreateDM(context.Background(), authenticatedRequest(credential, &spacev1.CreateDMRequest{
		RequestId: uuid.NewString(), Peer: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: owner.ID},
	})); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("self dm code = %v, error = %v", connectCode(err), err)
	}

	rootRequestID := uuid.NewString()
	rootResponse, err := client.SendMessage(context.Background(), authenticatedRequest(credential, &spacev1.SendMessageRequest{
		RequestId:         rootRequestID,
		Target:            &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: group.GetId()}},
		Body:              "# root\n\nMarkdown body",
		MentionedAgentIds: []string{agent.ID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	root := rootResponse.Msg.GetMessage()
	if root.GetRequestId() != rootRequestID || root.GetTargetSequence() != 1 || root.GetThreadRootMessageId() != "" ||
		!reflect.DeepEqual(root.GetMentionedAgentIds(), []string{agent.ID}) {
		t.Fatalf("root response = %+v", root)
	}
	replyResponse, err := client.SendMessage(context.Background(), authenticatedRequest(credential, &spacev1.SendMessageRequest{
		RequestId: uuid.NewString(),
		Target:    &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: root.GetId()}},
		Body:      "reply",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if replyResponse.Msg.GetMessage().GetThreadRootMessageId() != root.GetId() || replyResponse.Msg.GetMessage().GetTargetSequence() != 1 {
		t.Fatalf("reply response = %+v", replyResponse.Msg.GetMessage())
	}
	threadResponse, err := client.GetThread(context.Background(), authenticatedRequest(credential, &spacev1.GetThreadRequest{ThreadRootMessageId: root.GetId()}))
	if err != nil || threadResponse.Msg.GetThread().GetId() != root.GetId() {
		t.Fatalf("thread response = %+v, %v", threadResponse, err)
	}
	threadMessages, err := client.ListMessages(context.Background(), authenticatedRequest(credential, &spacev1.ListMessagesRequest{
		Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: root.GetId()}},
	}))
	if err != nil || len(threadMessages.Msg.GetMessages()) != 1 || threadMessages.Msg.GetMessages()[0].GetId() != replyResponse.Msg.GetMessage().GetId() {
		t.Fatalf("thread message list = %+v, %v", threadMessages, err)
	}

	archiveResponse, err := client.ArchiveSpace(context.Background(), authenticatedRequest(credential, &spacev1.ArchiveSpaceRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
	}))
	if err != nil || archiveResponse.Msg.GetReceipt().GetRequestId() == "" {
		t.Fatalf("archive response = %+v, %v", archiveResponse, err)
	}
	if _, err := client.SendMessage(context.Background(), authenticatedRequest(credential, &spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: group.GetId()}}, Body: "blocked",
	})); connectCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("archived send code = %v, error = %v", connectCode(err), err)
	}
	if _, err := client.GetSpace(context.Background(), authenticatedRequest(credential, &spacev1.GetSpaceRequest{SpaceId: group.GetId()})); err != nil {
		t.Fatalf("archived group read: %v", err)
	}

	payload, err := protojson.Marshal(&spacev1.ListSpacesResponse{Spaces: []*spacev1.Space{group, dmResponse.Msg.GetSpace()}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), credential) {
		t.Fatal("Connect collaboration response leaked the credential")
	}
}

func authenticatedRequest[T any](credential string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+credential)
	return request
}

func connectCode(err error) connect.Code {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return connect.CodeUnknown
	}
	return connectErr.Code()
}
