package computerhost

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
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	"github.com/abcdlsj/sumi/internal/placementcode"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/abcdlsj/sumi/internal/workspace"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSyncOnceReusesWorkspaceAfterProvisionBeforeAckCrash(t *testing.T) {
	serverRoot := t.TempDir()
	computerRoot := filepath.Join(t.TempDir(), "computer")
	key := "host-restart-registration-key"
	api := openHostTestServer(t, serverRoot)
	agentID, computerID := createPendingAssignment(t, api, key, "host-restart-agent")
	workspacePath, err := workspace.Provision(computerRoot, agentID)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspacePath, "before-ack.txt")
	if err := os.WriteFile(marker, []byte("survives"), 0o600); err != nil {
		t.Fatal(err)
	}

	host := New(hostConfig(api.http.URL, computerRoot, key))
	result, err := host.SyncOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ComputerID != computerID || result.Assignments != 1 || result.Active != 1 || result.Failed != 0 {
		t.Fatalf("sync result = %+v", result)
	}
	assertWorkspaceState(t, workspacePath, marker)
	placement := getHostPlacement(t, api, agentID)
	if placement.GetGeneration() != 1 || placement.GetState() != placementv1.PlacementState_PLACEMENT_STATE_ACTIVE {
		t.Fatalf("placement = %v", placement)
	}
	api.close(t)

	api = openHostTestServer(t, serverRoot)
	restarted := New(hostConfig(api.http.URL, computerRoot, key))
	result, err = restarted.SyncOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ComputerID != computerID || result.Assignments != 0 {
		t.Fatalf("restart sync result = %+v", result)
	}
	placement = getHostPlacement(t, api, agentID)
	if placement.GetGeneration() != 1 || placement.GetState() != placementv1.PlacementState_PLACEMENT_STATE_ACTIVE {
		t.Fatalf("restart placement = %v", placement)
	}
	assertWorkspaceState(t, workspacePath, marker)
	api.close(t)
	assertServerFilesExcludePath(t, serverRoot, computerRoot)
}

func TestSyncOnceAcknowledgesStableProvisionFailure(t *testing.T) {
	serverRoot := t.TempDir()
	computerRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(computerRoot, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := "host-failure-registration-key"
	api := openHostTestServer(t, serverRoot)
	defer api.close(t)
	agentID, _ := createPendingAssignment(t, api, key, "host-failure-agent")

	result, err := New(hostConfig(api.http.URL, computerRoot, key)).SyncOnce(context.Background())
	if err == nil {
		t.Fatal("SyncOnce error = nil")
	}
	if result.Assignments != 1 || result.Active != 0 || result.Failed != 1 {
		t.Fatalf("sync result = %+v", result)
	}
	placement := getHostPlacement(t, api, agentID)
	if placement.GetState() != placementv1.PlacementState_PLACEMENT_STATE_FAILED || placement.GetErrorCode() != placementcode.WorkspaceRootInvalid {
		t.Fatalf("failed placement = %v", placement)
	}
	encoded, marshalErr := protojson.Marshal(placement)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(encoded, []byte(computerRoot)) {
		t.Fatalf("Server response leaked path: %s", encoded)
	}
}

type hostTestServer struct {
	app        *server.Server
	http       *httptest.Server
	agents     agentv1connect.AgentServiceClient
	computers  computerv1connect.ComputerServiceClient
	placements placementv1connect.PlacementServiceClient
}

func openHostTestServer(t *testing.T, dataRoot string) *hostTestServer {
	t.Helper()
	app, err := server.New(context.Background(), server.Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	return &hostTestServer{
		app:        app,
		http:       httpServer,
		agents:     agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL),
		computers:  computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL),
		placements: placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL),
	}
}

func (api *hostTestServer) close(t *testing.T) {
	t.Helper()
	api.http.Close()
	if err := api.app.Close(); err != nil {
		t.Fatal(err)
	}
}

func createPendingAssignment(t *testing.T, api *hostTestServer, key, agentName string) (string, string) {
	t.Helper()
	agent, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(),
		Name:      agentName,
		Driver:    agentv1.Driver_DRIVER_NATIVE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	computer, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: key,
		Name:            "Host test computer",
		Os:              computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:            computerv1.Architecture_ARCHITECTURE_AMD64,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentID := agent.Msg.GetAgent().GetId()
	computerID := computer.Msg.GetComputer().GetId()
	if _, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		AgentId:    agentID,
		ComputerId: computerID,
	})); err != nil {
		t.Fatal(err)
	}
	return agentID, computerID
}

func getHostPlacement(t *testing.T, api *hostTestServer, agentID string) *placementv1.AgentPlacement {
	t.Helper()
	response, err := api.placements.GetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.GetAgentPlacementRequest{AgentId: agentID}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetPlacement()
}

func hostConfig(serverURL, dataRoot, key string) Config {
	return Config{
		ServerURL:       serverURL,
		DataRoot:        dataRoot,
		RegistrationKey: key,
		Name:            "Host test computer",
		OS:              computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:            computerv1.Architecture_ARCHITECTURE_AMD64,
	}
}

func assertWorkspaceState(t *testing.T, workspacePath, marker string) {
	t.Helper()
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "survives" {
		t.Fatalf("marker content = %q", content)
	}
	for _, path := range []string{
		filepath.Dir(filepath.Dir(filepath.Dir(workspacePath))),
		filepath.Dir(filepath.Dir(workspacePath)),
		filepath.Dir(workspacePath),
		workspacePath,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %v", path, info.Mode())
		}
	}
}

func assertServerFilesExcludePath(t *testing.T, serverRoot, localPath string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(serverRoot, "data"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(serverRoot, "data", entry.Name()))
		if err != nil {
			continue
		}
		if bytes.Contains(content, []byte(localPath)) {
			t.Fatalf("Server file %s contains local path", entry.Name())
		}
	}
}
