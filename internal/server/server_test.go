package server

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	server, err := New(context.Background(), Config{DataRoot: dataRoot})
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

func TestServesProductionWeb(t *testing.T) {
	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<main>Sumi production shell</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := New(context.Background(), Config{DataRoot: t.TempDir(), WebRoot: webRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	response, err := httpServer.Client().Get(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	httpServer.Close()
	if closeErr := server.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Sumi production shell") {
		t.Fatalf("unexpected body: %s", body)
	}
}
