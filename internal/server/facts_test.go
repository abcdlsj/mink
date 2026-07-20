package server

import (
	"context"
	"fmt"
	"net/http/httptest"
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
)

func TestComputerRegistrationConcurrencyAndRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	key := "trusted-local-computer-key"
	request := &computerv1.RegisterComputerRequest{
		RegistrationKey: key,
		Name:            "Build host",
		Os:              computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS,
		Arch:            computerv1.Architecture_ARCHITECTURE_ARM64,
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
	restarted, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: key,
		Name:            "Primary build host",
		Os:              computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:            computerv1.Architecture_ARCHITECTURE_AMD64,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Msg.GetComputer().GetId(); got != firstID {
		t.Fatalf("computer id changed across restart from %q to %q", firstID, got)
	}
	if got := restarted.Msg.GetComputer().GetCreatedAt().AsTime(); !got.Equal(createdAt) {
		t.Fatalf("created_at changed across restart from %s to %s", createdAt, got)
	}
	if restarted.Msg.GetComputer().GetName() != "Primary build host" || restarted.Msg.GetComputer().GetOs() != computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX || restarted.Msg.GetComputer().GetArch() != computerv1.Architecture_ARCHITECTURE_AMD64 {
		t.Fatalf("computer fields were not refreshed: %v", restarted.Msg.GetComputer())
	}
	persisted, err := api.computers.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: "{" + firstID + "}"}))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Msg.GetComputer().GetName() != "Primary build host" {
		t.Fatalf("get after restart returned %v", persisted.Msg.GetComputer())
	}

	other, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: "another-trusted-local-key",
		Name:            "Auxiliary host",
		Os:              computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS,
		Arch:            computerv1.Architecture_ARCHITECTURE_ARM64,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if other.Msg.GetComputer().GetId() == firstID {
		t.Fatal("different registration keys received the same computer id")
	}

	listed, err := api.computers.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := computerNames(listed.Msg.GetComputers()); fmt.Sprint(got) != "[Auxiliary host Primary build host]" {
		t.Fatalf("computer order = %v", got)
	}
	api.close(t)
}

func TestComputerHeartbeatRejectsMismatchedKeyWithoutMutation(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	key := "heartbeat-registration-key"
	registered, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: key,
		Name:            "Heartbeat host",
		Os:              computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:            computerv1.Architecture_ARCHITECTURE_AMD64,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := registered.Msg.GetComputer().GetId()
	before := registered.Msg.GetComputer().GetLastSeenAt().AsTime()

	wrongKey := "wrong-registration-key"
	_, err = api.computers.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId:      id,
		RegistrationKey: wrongKey,
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
		ComputerId:      id,
		RegistrationKey: key,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := heartbeat.Msg.GetComputer().GetLastSeenAt().AsTime(); !got.After(before) {
		t.Fatalf("heartbeat last_seen_at = %s, want after %s", got, before)
	}

	_, err = api.computers.HeartbeatComputer(context.Background(), connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId:      uuid.NewString(),
		RegistrationKey: key,
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
	_, err := api.computers.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: "not-a-uuid"}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestAgentCreationConcurrencyAndRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	requestID := "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"
	request := &agentv1.CreateAgentRequest{
		RequestId:   requestID,
		Name:        "release-coordinator",
		Description: "Coordinates release evidence",
		Driver:      agentv1.Driver_DRIVER_CODEX,
	}

	ids := createAgentsConcurrently(t, api.agents, request, 20)
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("concurrent agent creation returned %q and %q", ids[0], id)
		}
	}
	firstID := ids[0]
	idempotent, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId:   strings.ToLower(requestID),
		Name:        request.Name,
		Description: request.Description,
		Driver:      request.Driver,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := idempotent.Msg.GetAgent().GetId(); got != firstID {
		t.Fatalf("equivalent request UUID created %q after %q", got, firstID)
	}

	_, err = api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId:   requestID,
		Name:        request.Name,
		Description: "different payload",
		Driver:      request.Driver,
	}))
	assertConnectCode(t, err, connect.CodeAlreadyExists)
	unchanged, err := api.agents.GetAgent(context.Background(), connect.NewRequest(&agentv1.GetAgentRequest{AgentId: firstID}))
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Msg.GetAgent().GetDescription() != request.Description {
		t.Fatalf("conflicting request changed agent: %v", unchanged.Msg.GetAgent())
	}
	_, err = api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId:   uuid.NewString(),
		Name:        request.Name,
		Description: request.Description,
		Driver:      request.Driver,
	}))
	assertConnectCode(t, err, connect.CodeAlreadyExists)

	api.close(t)
	api = openFactsAPI(t, dataRoot)
	got, err := api.agents.GetAgent(context.Background(), connect.NewRequest(&agentv1.GetAgentRequest{AgentId: "{" + firstID + "}"}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Msg.GetAgent().GetName() != request.Name || got.Msg.GetAgent().GetDriver() != request.Driver {
		t.Fatalf("agent changed across restart: %v", got.Msg.GetAgent())
	}

	second, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(),
		Name:      "artifact-reviewer",
		Driver:    agentv1.Driver_DRIVER_CLAUDE,
	}))
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
		return &agentv1.CreateAgentRequest{
			RequestId: uuid.NewString(),
			Name:      "valid-agent",
			Driver:    agentv1.Driver_DRIVER_NATIVE,
		}
	}
	tests := []*agentv1.CreateAgentRequest{
		{Name: "valid-agent", Driver: agentv1.Driver_DRIVER_NATIVE},
		{RequestId: uuid.NewString(), Name: "", Driver: agentv1.Driver_DRIVER_NATIVE},
		{RequestId: uuid.NewString(), Name: "Invalid-Agent", Driver: agentv1.Driver_DRIVER_NATIVE},
		{RequestId: uuid.NewString(), Name: "trailing-", Driver: agentv1.Driver_DRIVER_NATIVE},
		{RequestId: uuid.NewString(), Name: strings.Repeat("a", 33), Driver: agentv1.Driver_DRIVER_NATIVE},
		{RequestId: uuid.NewString(), Name: "valid-agent"},
		{RequestId: uuid.NewString(), Name: "valid-agent", Driver: agentv1.Driver(99)},
	}
	tooLong := validRequest()
	tooLong.Description = strings.Repeat("a", 1001)
	tests = append(tests, tooLong)
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
	app        *Server
	http       *httptest.Server
	computers  computerv1connect.ComputerServiceClient
	agents     agentv1connect.AgentServiceClient
	placements placementv1connect.PlacementServiceClient
}

func openFactsAPI(t *testing.T, dataRoot string) *factsAPI {
	t.Helper()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	authorization := ownerClientAuthorization(t, dataRoot)
	return &factsAPI{
		app:        app,
		http:       httpServer,
		computers:  computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL),
		agents:     agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL, authorization),
		placements: placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL, authorization),
	}
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
		names = append(names, agent.GetName())
	}
	return names
}
