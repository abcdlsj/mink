package host

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
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	placementfailure "github.com/abcdlsj/sumi/internal/placement/failure"
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
	saveHostTestIdentity(t, localState, computerstate.Identity{
		ServerURL: api.http.URL, ComputerID: computerID, RegistrationKey: key, PairedAt: time.Now(),
	})
	config := hostConfig(api.http.URL, computerRoot, key)
	config.State = localState
	host := New(config)
	result, err := host.SyncOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ComputerID != computerID || result.Assignments != 1 || result.Ready != 1 || result.Failed != 0 {
		t.Fatalf("sync result = %+v", result)
	}
	computers, err := api.ownerComputers.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 {
		t.Fatalf("registered capability inventory = %+v, %v", computers.Msg.GetComputers(), err)
	}
	assertCapabilityInventory(t, computers.Msg.GetComputers()[0].GetCapabilityInventory())
	assertWorkspaceState(t, workspacePath, marker)
	placement := getHostPlacement(t, api, agentID)
	if placement.GetDesiredRevision() != 1 || placement.GetState() != placementv1.PlacementState_PLACEMENT_STATE_READY {
		t.Fatalf("placement = %v", placement)
	}
	identity, found, err := localState.Identity(context.Background())
	if err != nil || !found || identity.ComputerID != computerID || identity.RegistrationKey != key {
		t.Fatalf("persisted identity = %+v, %v, %v", identity, found, err)
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
		t.Fatalf("persisted identity restart = %+v, %v", importRestart, err)
	}
	if err := localState.Close(); err != nil {
		t.Fatal(err)
	}
	assertWorkspaceState(t, workspacePath, marker)
	api.close(t)
	assertServerFilesExcludePath(t, serverRoot, computerRoot)
}

func assertCapabilityInventory(t *testing.T, inventory *computerv1.CapabilityInventory) {
	t.Helper()
	if inventory == nil || inventory.GetRevision() == 0 || len(inventory.GetEngines()) != 3 || len(inventory.GetSandboxes()) != 1 {
		t.Fatalf("capability inventory = %+v", inventory)
	}
	if inventory.GetEngines()[0].GetEngine() != agentv1.EngineKind_ENGINE_KIND_BUILTIN ||
		inventory.GetEngines()[0].GetHealth() != computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE ||
		inventory.GetCredentialDelivery().GetHealth() != computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE {
		t.Fatalf("builtin capability = %+v", inventory.GetEngines()[0])
	}
	if sandbox := inventory.GetSandboxes()[0]; sandbox.GetProvider() != computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL ||
		sandbox.GetFilesystemIsolation() != computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE {
		t.Fatalf("trusted-local capability = %+v", sandbox)
	}
}

func mustHostCapabilityInventory(t *testing.T) *computerv1.CapabilityInventoryDeclaration {
	t.Helper()
	inventory, err := CapabilityInventory()
	if err != nil {
		t.Fatal(err)
	}
	return inventory
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
	agentID, computerID := createPendingAssignment(t, api, key, "host-failure-agent")

	state, err := computerstate.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	saveHostTestIdentity(t, state, computerstate.Identity{
		ServerURL: api.http.URL, ComputerID: computerID, RegistrationKey: key, PairedAt: time.Now(),
	})
	config := hostConfig(api.http.URL, computerRoot, key)
	config.State = state
	result, err := New(config).SyncOnce(context.Background())
	if err == nil {
		t.Fatal("SyncOnce error = nil")
	}
	if result.Assignments != 1 || result.Ready != 0 || result.Failed != 1 {
		t.Fatalf("sync result = %+v", result)
	}
	placement := getHostPlacement(t, api, agentID)
	if placement.GetState() != placementv1.PlacementState_PLACEMENT_STATE_FAILED || placement.GetErrorCode() != placementfailure.WorkspaceRootInvalid {
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
		RequestId: uuid.NewString(), Handle: agentName, DisplayName: agentName,
		Role: "worker", Mission: "Exercise computer host behavior",
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
	inventory := mustHostCapabilityInventory(t)
	for _, engine := range inventory.GetEngines() {
		if engine.GetEngine() == agentv1.EngineKind_ENGINE_KIND_BUILTIN {
			engine.Health = computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY
			engine.SupportsToolCalls = true
			engine.SupportsCancel = true
			engine.ProviderProtocols = []agentv1.ProviderProtocol{agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES}
		}
	}
	inventory.CredentialDelivery = &computerv1.CredentialDeliveryCapability{
		Health:    computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY,
		Algorithm: computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305,
		Store:     computerv1.CredentialStore_CREDENTIAL_STORE_LINUX_SECRET_SERVICE,
		KeyId:     "host-test-key", PublicKey: make([]byte, 32),
	}
	computer, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey:     key,
		Name:                "Host test computer",
		Os:                  computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:                computerv1.Architecture_ARCHITECTURE_AMD64,
		RequestId:           uuid.NewString(),
		PairingToken:        token,
		CapabilityInventory: inventory,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentID := agent.Msg.GetAgent().GetId()
	computerID := computer.Msg.GetComputer().GetId()
	delivery, err := api.ownerComputers.EnqueueCredentialDelivery(context.Background(), connect.NewRequest(&computerv1.EnqueueCredentialDeliveryRequest{
		RequestId: uuid.NewString(), ComputerId: computerID, AgentId: agentID,
		CredentialKind: computerv1.CredentialKind_CREDENTIAL_KIND_OPENAI,
		SealedCredential: &computerv1.SealedCredential{
			Algorithm: computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305,
			KeyId:     "host-test-key", EphemeralPublicKey: make([]byte, 32), Nonce: make([]byte, 24), Ciphertext: make([]byte, 17),
		},
		ExpiresAt: timestamppb.New(time.Now().Add(5 * time.Minute)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := api.computers.ClaimCredentialDelivery(context.Background(), connect.NewRequest(&computerv1.ClaimCredentialDeliveryRequest{
		ComputerId: computerID, RegistrationKey: key,
	}))
	if err != nil || claimed.Msg.GetDelivery().GetId() != delivery.Msg.GetDelivery().GetId() {
		t.Fatalf("credential claim = %+v, %v", claimed, err)
	}
	bindingHandle := "cred_host_test_" + agentID
	completed, err := api.computers.CompleteCredentialDelivery(context.Background(), connect.NewRequest(&computerv1.CompleteCredentialDeliveryRequest{
		ComputerId: computerID, RegistrationKey: key, DeliveryId: delivery.Msg.GetDelivery().GetId(), BindingHandle: bindingHandle,
	}))
	if err != nil || completed.Msg.GetDelivery().GetState() != computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_SUCCEEDED {
		t.Fatalf("credential completion = %+v, %v", completed, err)
	}
	if _, err := api.agents.UpdateAgentRuntimeSpec(context.Background(), connect.NewRequest(&agentv1.UpdateAgentRuntimeSpecRequest{
		RequestId: uuid.NewString(), AgentId: agentID, Engine: agentv1.EngineKind_ENGINE_KIND_BUILTIN,
		ProviderProtocol: agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES,
		ProviderEndpoint: "https://provider.invalid/v1", Model: "test-model", CredentialBindingHandle: bindingHandle,
		SandboxProvider:       agentv1.RuntimeSandboxProvider_RUNTIME_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		MaxRunDurationSeconds: 120, MaxOutputBytes: 1 << 20,
		ToolPolicy: &agentv1.RuntimeToolPolicy{Message: true},
	})); err != nil {
		t.Fatal(err)
	}
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

func saveHostTestIdentity(t *testing.T, state *computerstate.State, identity computerstate.Identity) {
	t.Helper()
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePairingAttempt(context.Background(), computerstate.PairingAttempt{
		ServerURL: identity.ServerURL, PairingToken: base64.RawURLEncoding.EncodeToString(rawToken),
		RequestID: uuid.NewString(), RegistrationKey: identity.RegistrationKey, CreatedAt: identity.PairedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.CompletePairing(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
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
