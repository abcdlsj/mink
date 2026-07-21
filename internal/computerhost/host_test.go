package computerhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/placementcode"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/abcdlsj/sumi/internal/workspace"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
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

	localState, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	config := hostConfig(api.http.URL, computerRoot, key)
	config.State = localState
	host := New(config)
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
	identity, found, err := localState.Identity(context.Background())
	if err != nil || !found || identity.ComputerID != computerID || identity.RegistrationKey != key {
		t.Fatalf("imported legacy identity = %+v, %v, %v", identity, found, err)
	}
	if err := localState.Close(); err != nil {
		t.Fatal(err)
	}
	localState, err = computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	importRestartConfig := hostConfig(api.http.URL, computerRoot, "")
	importRestartConfig.State = localState
	importRestart, err := New(importRestartConfig).SyncOnce(context.Background())
	if err != nil || importRestart.ComputerID != computerID || importRestart.Assignments != 0 {
		t.Fatalf("legacy identity restart = %+v, %v", importRestart, err)
	}
	if err := localState.Close(); err != nil {
		t.Fatal(err)
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
	app            *server.Server
	http           *httptest.Server
	agents         agentv1connect.AgentServiceClient
	computers      computerv1connect.ComputerServiceClient
	ownerComputers computerv1connect.ComputerServiceClient
	placements     placementv1connect.PlacementServiceClient
}

func openHostTestServer(t *testing.T, dataRoot string) *hostTestServer {
	t.Helper()
	app, err := server.New(context.Background(), server.Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		httpServer.Close()
		app.Close()
		t.Fatal(err)
	}
	authorization := clientAuthorization(credential)
	return &hostTestServer{
		app:       app,
		http:      httpServer,
		agents:    agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL, authorization),
		computers: computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL),
		ownerComputers: computerv1connect.NewComputerServiceClient(
			httpServer.Client(), httpServer.URL, authorization,
		),
		placements: placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL, authorization),
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
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	if _, err := api.ownerComputers.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	computer, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: key,
		Name:            "Host test computer",
		Os:              computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:            computerv1.Architecture_ARCHITECTURE_AMD64,
		RequestId:       uuid.NewString(),
		PairingToken:    token,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentID := agent.Msg.GetAgent().GetId()
	computerID := computer.Msg.GetComputer().GetId()
	if _, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId:  uuid.NewString(),
		AgentId:    agentID,
		ComputerId: computerID,
	})); err != nil {
		t.Fatal(err)
	}
	return agentID, computerID
}

func clientAuthorization(credential string) connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+credential)
			return next(ctx, request)
		}
	}))
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
