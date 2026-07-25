package collaboration

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
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
	now := time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC)
	password := "collaboration-password-1234567890"
	digest, err := localauth.HashPassword(rand.Reader, password, localauth.DefaultPasswordParameters())
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "collaboration-session-token-ABCDEFGHIJKLMN" // 43 characters
	bootstrap, err := database.RegisterFirstOwner(context.Background(), authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity:         authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password:         digest,
		SessionToken:     sessionToken,
		Now:              now,
		SessionExpiresAt: now.Add(12 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	peerDigest, err := localauth.HashPassword(rand.Reader, "peer-password-1234567890", localauth.DefaultPasswordParameters())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := database.CreateHuman(context.Background(), store.CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "HTTP Peer", Role: "member",
		Identity: authorityapp.AuthenticationIdentity{Provider: "local", Subject: "httppeer"},
		Password: peerDigest, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	service := New(database, "")
	service.now = func() time.Time { return now.Add(time.Minute) }
	path, handler := spacev1connect.NewCollaborationServiceHandler(service, connect.WithInterceptors(authority.NewBrowserInterceptor(database, authority.BrowserInterceptorConfig{})))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	authClient := newSessionClient(server, sessionToken)
	client := spacev1connect.NewCollaborationServiceClient(server.Client(), server.URL)

	unauthenticated := connect.NewRequest(&spacev1.CreateGroupRequest{RequestId: uuid.NewString(), Name: "unauthenticated"})
	if _, err := client.CreateGroup(context.Background(), unauthenticated); connectCode(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated create code = %v, error = %v", connectCode(err), err)
	}

	groupRequestID := uuid.NewString()
	groupResponse, err := authClient.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: groupRequestID, Name: "connect-lab",
	}))
	if err != nil {
		t.Fatal(err)
	}
	group := groupResponse.Msg.GetSpace()
	if group.GetId() == "" || group.GetOrganizationId() != bootstrap.Organization.ID || group.GetKind() != spacev1.SpaceKind_SPACE_KIND_GROUP {
		t.Fatalf("group response = %+v", group)
	}
	if _, err := authClient.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: " surrounding ",
	})); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid group name code = %v, error = %v", connectCode(err), err)
	}
	agent, err := database.CreateAgent(context.Background(), store.CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Handle: "collaboration-mention", DisplayName: "Collaboration Mention", Role: "collaborator", Mission: "Respond to mentions", Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authClient.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.ID},
	})); err != nil {
		t.Fatal(err)
	}

	dmResponse, err := authClient.CreateDM(context.Background(), connect.NewRequest(&spacev1.CreateDMRequest{
		RequestId: uuid.NewString(), Peer: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: peer.ID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if dmResponse.Msg.GetSpace().GetKind() != spacev1.SpaceKind_SPACE_KIND_DM {
		t.Fatalf("dm response = %+v", dmResponse.Msg.GetSpace())
	}
	if _, err := authClient.CreateDM(context.Background(), connect.NewRequest(&spacev1.CreateDMRequest{
		RequestId: uuid.NewString(), Peer: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: owner.ID},
	})); connectCode(err) != connect.CodeInvalidArgument {
		t.Fatalf("self dm code = %v, error = %v", connectCode(err), err)
	}

	rootRequestID := uuid.NewString()
	rootResponse, err := authClient.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId:           rootRequestID,
		Target:              &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: group.GetId()}},
		Body:                "# root\n\nMarkdown body",
		MentionedPrincipals: []*spacev1.Principal{{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.ID}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	root := rootResponse.Msg.GetMessage()
	if root.GetRequestId() != rootRequestID || root.GetTargetSequence() != 1 || root.GetThreadRootMessageId() != "" ||
		len(root.GetMentionedPrincipals()) != 1 || root.GetMentionedPrincipals()[0].GetKind() != spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT ||
		root.GetMentionedPrincipals()[0].GetId() != agent.ID {
		t.Fatalf("root response = %+v", root)
	}
	replyResponse, err := authClient.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
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
	threadResponse, err := authClient.GetThread(context.Background(), connect.NewRequest(&spacev1.GetThreadRequest{ThreadRootMessageId: root.GetId()}))
	if err != nil || threadResponse.Msg.GetThread().GetId() != root.GetId() {
		t.Fatalf("thread response = %+v, %v", threadResponse, err)
	}
	threadMessages, err := authClient.ListMessages(context.Background(), connect.NewRequest(&spacev1.ListMessagesRequest{
		Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: root.GetId()}},
	}))
	if err != nil || len(threadMessages.Msg.GetMessages()) != 1 || threadMessages.Msg.GetMessages()[0].GetId() != replyResponse.Msg.GetMessage().GetId() {
		t.Fatalf("thread message list = %+v, %v", threadMessages, err)
	}

	archiveResponse, err := authClient.ArchiveSpace(context.Background(), connect.NewRequest(&spacev1.ArchiveSpaceRequest{
		RequestId: uuid.NewString(), SpaceId: group.GetId(),
	}))
	if err != nil || archiveResponse.Msg.GetReceipt().GetRequestId() == "" {
		t.Fatalf("archive response = %+v, %v", archiveResponse, err)
	}
	if _, err := authClient.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: group.GetId()}}, Body: "blocked",
	})); connectCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("archived send code = %v, error = %v", connectCode(err), err)
	}
	if _, err := authClient.GetSpace(context.Background(), connect.NewRequest(&spacev1.GetSpaceRequest{SpaceId: group.GetId()})); err != nil {
		t.Fatalf("archived group read: %v", err)
	}

	payload, err := protojson.Marshal(&spacev1.ListSpacesResponse{Spaces: []*spacev1.Space{group, dmResponse.Msg.GetSpace()}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), password) || strings.Contains(string(payload), sessionToken) {
		t.Fatal("Connect collaboration response leaked credentials")
	}
}

func newSessionClient(server *httptest.Server, sessionToken string) spacev1connect.CollaborationServiceClient {
	return spacev1connect.NewCollaborationServiceClient(server.Client(), server.URL, connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Cookie", "sumi_browser_session="+sessionToken)
			request.Header().Set("Origin", "http://127.0.0.1:8080")
			return next(ctx, request)
		}
	})))
}

func connectCode(err error) connect.Code {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return connect.CodeUnknown
	}
	return connectErr.Code()
}
