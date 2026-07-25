package server

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestComputerPairingCreateConsumeReplayAndQuiet(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	createRequest := &computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	}
	created, err := api.computers.CreateComputerPairing(context.Background(), connect.NewRequest(createRequest))
	if err != nil {
		t.Fatal(err)
	}
	replayedCreate, err := api.computers.CreateComputerPairing(context.Background(), connect.NewRequest(createRequest))
	if err != nil || replayedCreate.Msg.GetPairingId() != created.Msg.GetPairingId() ||
		!replayedCreate.Msg.GetExpiresAt().AsTime().Equal(created.Msg.GetExpiresAt().AsTime()) {
		t.Fatalf("pairing creation replay = %+v, %v", replayedCreate, err)
	}
	conflictingToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	_, err = api.computers.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: createRequest.GetRequestId(), PairingToken: conflictingToken, ExpiresAt: createRequest.GetExpiresAt(),
	}))
	assertConnectCode(t, err, connect.CodeAlreadyExists)
	_, err = api.computers.CreateComputerPairing(context.Background(), connect.NewRequest(createRequest))
	assertConnectCode(t, err, connect.CodeUnauthenticated)

	registrationKey := "pairing-registration-secret"
	registerRequest := &computerv1.RegisterComputerRequest{
		RegistrationKey: registrationKey, Name: "Paired host",
		Os: computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, Arch: computerv1.Architecture_ARCHITECTURE_ARM64,
		RequestId: uuid.NewString(), PairingToken: token, CapabilityInventory: serverTestCapabilityInventory(),
	}
	registered, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(registerRequest))
	if err != nil {
		t.Fatal(err)
	}
	replayedRegister, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(registerRequest))
	if err != nil || replayedRegister.Msg.GetComputer().GetId() != registered.Msg.GetComputer().GetId() {
		t.Fatalf("pairing consume replay = %+v, %v", replayedRegister, err)
	}
	conflictingRegister := proto.Clone(registerRequest).(*computerv1.RegisterComputerRequest)
	conflictingRegister.RequestId = uuid.NewString()
	_, err = api.computers.RegisterComputer(context.Background(), connect.NewRequest(conflictingRegister))
	assertConnectCode(t, err, connect.CodeAlreadyExists)

	for _, suffix := range []string{"", "-wal", "-shm"} {
		payload, readErr := os.ReadFile(filepath.Join(dataRoot, "data", "server.db"+suffix))
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		if bytes.Contains(payload, []byte(token)) || bytes.Contains(payload, []byte(registrationKey)) {
			t.Fatalf("server sqlite %q contains raw computer credential", suffix)
		}
	}
}

func TestComputerRegistrationConcurrencyAndRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	key := "trusted-local-computer-key"
	request := &computerv1.RegisterComputerRequest{
		RegistrationKey:     key,
		Name:                "Build host",
		Os:                  computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS,
		Arch:                computerv1.Architecture_ARCHITECTURE_ARM64,
		RequestId:           uuid.NewString(),
		PairingToken:        createPairingToken(t, api.computers),
		CapabilityInventory: serverTestCapabilityInventory(),
	}

	ids := registerComputersConcurrently(t, api.computers, request, 20)
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("concurrent registration returned %q and %q", ids[0], id)
		}
	}

	first, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := protojson.Marshal(first.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "registrationKey") {
		t.Fatalf("registration key leaked in response: %s", encoded)
	}
	firstID := first.Msg.GetComputer().GetId()
	createdAt := first.Msg.GetComputer().GetCreatedAt().AsTime()
	api.close(t)

	api = openFactsAPI(t, dataRoot)
	restarted, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Msg.GetComputer().GetId(); got != firstID {
		t.Fatalf("computer id changed across restart from %q to %q", firstID, got)
	}
	if got := restarted.Msg.GetComputer().GetCreatedAt().AsTime(); !got.Equal(createdAt) {
		t.Fatalf("created_at changed across restart from %s to %s", createdAt, got)
	}
	if restarted.Msg.GetComputer().GetName() != request.GetName() || restarted.Msg.GetComputer().GetOs() != request.GetOs() || restarted.Msg.GetComputer().GetArch() != request.GetArch() {
		t.Fatalf("pairing replay changed immutable registration facts: %v", restarted.Msg.GetComputer())
	}
	persisted, err := api.computers.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: "{" + firstID + "}"}))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Msg.GetComputer().GetName() != request.GetName() {
		t.Fatalf("get after restart returned %v", persisted.Msg.GetComputer())
	}

	other := pairComputer(t, api, "another-trusted-local-key", "Auxiliary host", computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64)
	if other.Msg.GetComputer().GetId() == firstID {
		t.Fatal("different registration keys received the same computer id")
	}

	listed, err := api.computers.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := computerNames(listed.Msg.GetComputers()); fmt.Sprint(got) != "[Auxiliary host Build host]" {
		t.Fatalf("computer order = %v", got)
	}
	api.close(t)
}

func TestComputerHeartbeatRejectsMismatchedKeyWithoutMutation(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	key := "heartbeat-registration-key"
	registered := pairComputer(t, api, key, "Heartbeat host", computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, computerv1.Architecture_ARCHITECTURE_AMD64)
	id := registered.Msg.GetComputer().GetId()
	before := registered.Msg.GetComputer().GetLastSeenAt().AsTime()

	wrongKey := "wrong-registration-key"
	_, err := api.computers.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId:          id,
		RegistrationKey:     wrongKey,
		CapabilityInventory: serverTestCapabilityInventory(),
	}))
	assertConnectCode(t, err, connect.CodePermissionDenied)
	if strings.Contains(err.Error(), wrongKey) {
		t.Fatalf("registration key leaked in error: %v", err)
	}
	current, err := api.computers.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: id}))
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Msg.GetComputer().GetLastSeenAt().AsTime(); !got.Equal(before) {
		t.Fatalf("wrong key changed last_seen_at from %s to %s", before, got)
	}

	time.Sleep(time.Millisecond)
	heartbeat, err := api.computers.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId:          id,
		RegistrationKey:     key,
		CapabilityInventory: serverTestCapabilityInventory(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := heartbeat.Msg.GetComputer().GetLastSeenAt().AsTime(); !got.After(before) {
		t.Fatalf("heartbeat last_seen_at = %s, want after %s", got, before)
	}

	_, err = api.computers.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId:          uuid.NewString(),
		RegistrationKey:     key,
		CapabilityInventory: serverTestCapabilityInventory(),
	}))
	assertConnectCode(t, err, connect.CodeNotFound)
}

func TestComputerValidation(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	tests := []*computerv1.RegisterComputerRequest{
		{Name: "host", Os: computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, Arch: computerv1.Architecture_ARCHITECTURE_ARM64},
		{RegistrationKey: "key", Name: " host", Os: computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, Arch: computerv1.Architecture_ARCHITECTURE_ARM64},
		{RegistrationKey: "key", Name: "host", Arch: computerv1.Architecture_ARCHITECTURE_ARM64},
		{RegistrationKey: "key", Name: "host", Os: computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS},
		{RegistrationKey: "key", Name: "host", Os: computerv1.OperatingSystem(99), Arch: computerv1.Architecture_ARCHITECTURE_ARM64},
		{RegistrationKey: "key", Name: "host", Os: computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, Arch: computerv1.Architecture(99)},
	}
	for _, request := range tests {
		_, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(request))
		assertConnectCode(t, err, connect.CodeInvalidArgument)
	}
	_, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: "unpaired-key", Name: "unpaired", Os: computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch: computerv1.Architecture_ARCHITECTURE_ARM64,
	}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)
	_, err = api.computers.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: "not-a-uuid"}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestAgentCreationConcurrencyAndRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	requestID := "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"
	request := &agentv1.CreateAgentRequest{
		RequestId: requestID, Handle: "release-coordinator", DisplayName: "Release Coordinator",
		Role: "release coordinator", Mission: "Coordinate release evidence", Instructions: "Keep evidence explicit.",
	}

	ids := createAgentsConcurrently(t, api.agents, request, 20)
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("concurrent agent creation returned %q and %q", ids[0], id)
		}
	}
	firstID := ids[0]
	idempotent, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: strings.ToLower(requestID), Handle: request.Handle, DisplayName: request.DisplayName,
		Role: request.Role, Mission: request.Mission, Instructions: request.Instructions,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := idempotent.Msg.GetAgent().GetId(); got != firstID {
		t.Fatalf("equivalent request UUID created %q after %q", got, firstID)
	}

	_, err = api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: requestID, Handle: request.Handle, DisplayName: request.DisplayName,
		Role: request.Role, Mission: "Different mission", Instructions: request.Instructions,
	}))
	assertConnectCode(t, err, connect.CodeAlreadyExists)
	unchanged, err := api.agents.GetAgent(context.Background(), connect.NewRequest(&agentv1.GetAgentRequest{AgentId: firstID}))
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Msg.GetAgent().GetProfile().GetMission() != request.Mission {
		t.Fatalf("conflicting request changed agent: %v", unchanged.Msg.GetAgent())
	}
	_, err = api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Handle: request.Handle, DisplayName: request.DisplayName,
		Role: request.Role, Mission: request.Mission, Instructions: request.Instructions,
	}))
	assertConnectCode(t, err, connect.CodeAlreadyExists)

	api.close(t)
	api = openFactsAPI(t, dataRoot)
	got, err := api.agents.GetAgent(context.Background(), connect.NewRequest(&agentv1.GetAgentRequest{AgentId: "{" + firstID + "}"}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Msg.GetAgent().GetHandle() != request.Handle || got.Msg.GetAgent().GetProfile().GetRevision() != 1 {
		t.Fatalf("agent changed across restart: %v", got.Msg.GetAgent())
	}

	second, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(agentRequest("artifact-reviewer")))
	if err != nil {
		t.Fatal(err)
	}
	if second.Msg.GetAgent().GetId() == firstID {
		t.Fatal("different agent requests received the same agent id")
	}
	listed, err := api.agents.ListAgents(context.Background(), connect.NewRequest(&agentv1.ListAgentsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := agentNames(listed.Msg.GetAgents()); fmt.Sprint(got) != "[artifact-reviewer release-coordinator]" {
		t.Fatalf("agent order = %v", got)
	}
	api.close(t)
}

func TestAgentValidationAndNotFound(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	validRequest := func() *agentv1.CreateAgentRequest {
		return agentRequest("valid-agent")
	}
	missingRequestID := validRequest()
	missingRequestID.RequestId = ""
	emptyHandle := validRequest()
	emptyHandle.Handle = ""
	invalidHandle := validRequest()
	invalidHandle.Handle = "Invalid-Agent"
	trailingHandle := validRequest()
	trailingHandle.Handle = "trailing-"
	longHandle := validRequest()
	longHandle.Handle = strings.Repeat("a", 33)
	emptyDisplayName := validRequest()
	emptyDisplayName.DisplayName = ""
	emptyRole := validRequest()
	emptyRole.Role = ""
	emptyMission := validRequest()
	emptyMission.Mission = ""
	longDisplayName := validRequest()
	longDisplayName.DisplayName = strings.Repeat("界", 101)
	longRole := validRequest()
	longRole.Role = strings.Repeat("界", 201)
	longMission := validRequest()
	longMission.Mission = strings.Repeat("界", 2001)
	longInstructions := validRequest()
	longInstructions.Instructions = strings.Repeat("界", 20001)
	tests := []*agentv1.CreateAgentRequest{
		missingRequestID, emptyHandle, invalidHandle, trailingHandle, longHandle,
		emptyDisplayName, emptyRole, emptyMission, longDisplayName, longRole, longMission, longInstructions,
	}
	for _, request := range tests {
		_, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(request))
		assertConnectCode(t, err, connect.CodeInvalidArgument)
	}
	_, err := api.agents.GetAgent(context.Background(), connect.NewRequest(&agentv1.GetAgentRequest{AgentId: uuid.NewString()}))
	assertConnectCode(t, err, connect.CodeNotFound)
	_, err = api.agents.GetAgent(context.Background(), connect.NewRequest(&agentv1.GetAgentRequest{AgentId: "not-a-uuid"}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

type factsAPI struct {
	app              *Server
	http             *httptest.Server
	computers        computerv1connect.ComputerServiceClient
	agents           agentv1connect.AgentServiceClient
	placements       placementv1connect.PlacementServiceClient
	registrationKeys map[string]string
}

func openFactsAPI(t *testing.T, dataRoot string) *factsAPI {
	t.Helper()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	return &factsAPI{
		app:       app,
		http:      httpServer,
		computers: computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL),
		agents:           agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL),
		placements:       placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL),
		registrationKeys: make(map[string]string),
	}
}

func pairComputer(t *testing.T, api *factsAPI, key, name string, operatingSystem computerv1.OperatingSystem, architecture computerv1.Architecture) *connect.Response[computerv1.RegisterComputerResponse] {
	t.Helper()
	response := pairComputerClients(t, api.computers, api.computers, key, name, operatingSystem, architecture)
	if api.registrationKeys == nil {
		api.registrationKeys = make(map[string]string)
	}
	api.registrationKeys[response.Msg.GetComputer().GetId()] = key
	return response
}

func pairComputerClients(t *testing.T, owner, public computerv1connect.ComputerServiceClient, key, name string, operatingSystem computerv1.OperatingSystem, architecture computerv1.Architecture) *connect.Response[computerv1.RegisterComputerResponse] {
	t.Helper()
	request := &computerv1.RegisterComputerRequest{
		RegistrationKey: key, Name: name, Os: operatingSystem, Arch: architecture,
		RequestId: uuid.NewString(), PairingToken: createPairingToken(t, owner), CapabilityInventory: serverTestCapabilityInventory(),
	}
	response, err := public.RegisterComputer(context.Background(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func createPairingToken(t *testing.T, client computerv1connect.ComputerServiceClient) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	_, err := client.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func serverTestCapabilityInventory() *computerv1.CapabilityInventoryDeclaration {
	return &computerv1.CapabilityInventoryDeclaration{
		Engines: []*computerv1.EngineCapability{{
			Engine: agentv1.EngineKind_ENGINE_KIND_BUILTIN, Version: "test", ProtocolVersion: 1,
			SupportsToolCalls: true, SupportsCancel: true,
			ProviderProtocols: []agentv1.ProviderProtocol{agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES},
			Health:            computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY,
		}},
		Sandboxes: []*computerv1.SandboxCapability{{
			Provider:              computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL,
			Isolation:             computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL,
			WorkspaceAccess:       computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE,
			ProcessControl:        computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP,
			FilesystemIsolation:   computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE,
			NetworkIsolation:      computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE,
			SecretMaterialization: computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT,
			DaemonCrashCleanup:    computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE,
		}},
		CredentialDelivery: &computerv1.CredentialDeliveryCapability{
			Health:    computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY,
			Algorithm: computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305,
			Store:     computerv1.CredentialStore_CREDENTIAL_STORE_LINUX_SECRET_SERVICE,
			KeyId:     serverTestCredentialKeyID, PublicKey: serverTestCredentialPublicKey(),
		},
	}
}

const serverTestCredentialKeyID = "server-test-credential-key"

func serverTestCredentialPublicKey() []byte {
	privateBytes := make([]byte, 32)
	for index := range privateBytes {
		privateBytes[index] = byte(index + 1)
	}
	private, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		panic(err)
	}
	return private.PublicKey().Bytes()
}

func (api *factsAPI) close(t *testing.T) {
	t.Helper()
	api.http.Close()
	if err := api.app.Close(); err != nil {
		t.Fatal(err)
	}
}

func registerComputersConcurrently(t *testing.T, client computerv1connect.ComputerServiceClient, request *computerv1.RegisterComputerRequest, count int) []string {
	t.Helper()
	ids := make([]string, count)
	errors := make(chan error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := client.RegisterComputer(context.Background(), connect.NewRequest(request))
			if err != nil {
				errors <- err
				return
			}
			ids[index] = response.Msg.GetComputer().GetId()
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	return ids
}

func createAgentsConcurrently(t *testing.T, client agentv1connect.AgentServiceClient, request *agentv1.CreateAgentRequest, count int) []string {
	t.Helper()
	ids := make([]string, count)
	errors := make(chan error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := client.CreateAgent(context.Background(), connect.NewRequest(request))
			if err != nil {
				errors <- err
				return
			}
			ids[index] = response.Msg.GetAgent().GetId()
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	return ids
}

func assertConnectCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", code)
	}
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("error code = %s, want %s: %v", got, code, err)
	}
}

func computerNames(computers []*computerv1.Computer) []string {
	names := make([]string, 0, len(computers))
	for _, computer := range computers {
		names = append(names, computer.GetName())
	}
	return names
}

func agentNames(agents []*agentv1.Agent) []string {
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		names = append(names, agent.GetHandle())
	}
	return names
}
