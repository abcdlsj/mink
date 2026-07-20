package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
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
	encoded, err := protojson.Marshal(replyResponse.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(credential)) || bytes.Contains(encoded, []byte("credential")) {
		t.Fatalf("credential leaked in collaboration response: %s", encoded)
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
	app    *Server
	http   *httptest.Server
	client spacev1connect.CollaborationServiceClient
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
		client: spacev1connect.NewCollaborationServiceClient(httpServer.Client(), httpServer.URL),
	}
}

func (api collaborationAPI) close(t *testing.T) {
	t.Helper()
	api.http.Close()
	if err := api.app.Close(); err != nil {
		t.Fatal(err)
	}
}
