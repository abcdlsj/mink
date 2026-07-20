package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	systemv1 "github.com/abcdlsj/sumi/gen/go/sumi/system/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/system/v1/systemv1connect"
)

func TestBootstrapUsesPersistentIdentity(t *testing.T) {
	dataRoot := t.TempDir()
	first := requestBootstrap(t, dataRoot)
	second := requestBootstrap(t, dataRoot)
	if first.GetServerId() != second.GetServerId() {
		t.Fatalf("server id changed from %q to %q", first.GetServerId(), second.GetServerId())
	}
	if first.GetVersion() == "" {
		t.Fatal("version is empty")
	}
	if len(first.GetPlatforms()) != 2 {
		t.Fatalf("platforms = %v", first.GetPlatforms())
	}
}

func requestBootstrap(t *testing.T, dataRoot string) *systemv1.GetBootstrapResponse {
	t.Helper()
	server, err := New(context.Background(), dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	client := systemv1connect.NewSystemServiceClient(httpServer.Client(), httpServer.URL)
	response, err := client.GetBootstrap(context.Background(), connect.NewRequest(&systemv1.GetBootstrapRequest{}))
	httpServer.Close()
	if closeErr := server.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}
