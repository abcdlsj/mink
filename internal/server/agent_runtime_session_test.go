package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

func TestAgentRuntimeHTTPCreateRenewRevokeAndRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	client := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)

	first := createRuntimeOverHTTP(t, client, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
	second := createRuntimeOverHTTP(t, client, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
	assertStoreRuntimeRejected(t, api.app.store, first.GetToken())
	if _, err := api.app.store.AuthenticateAgentRuntimeSession(context.Background(), second.GetToken(), time.Now()); err != nil {
		t.Fatal(err)
	}

	api.close(t)
	api = openFactsAPI(t, dataRoot)
	client = runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
	renewed := renewRuntimeOverHTTP(t, client, second.GetToken(), computer.GetId(), registrationKey)
	assertStoreRuntimeRejected(t, api.app.store, second.GetToken())

	wrongKey := runtimeRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: computer.GetId(), RegistrationKey: "wrong-registration-key",
	}, renewed.GetToken())
	_, err := client.RenewAgentRuntimeSession(context.Background(), wrongKey)
	assertConnectCode(t, err, connect.CodePermissionDenied)
	if strings.Contains(err.Error(), wrongKey.Msg.GetRegistrationKey()) || strings.Contains(err.Error(), renewed.GetToken()) {
		t.Fatalf("runtime credential leaked in error: %v", err)
	}
	if _, err := api.app.store.AuthenticateAgentRuntimeSession(context.Background(), renewed.GetToken(), time.Now()); err != nil {
		t.Fatal(err)
	}

	revoke := runtimeRequest(&runtimev1.RevokeAgentRuntimeSessionRequest{
		ComputerId: computer.GetId(), RegistrationKey: registrationKey,
	}, renewed.GetToken())
	if _, err := client.RevokeAgentRuntimeSession(context.Background(), revoke); err != nil {
		t.Fatal(err)
	}
	assertStoreRuntimeRejected(t, api.app.store, renewed.GetToken())
	_, err = client.RevokeAgentRuntimeSession(context.Background(), revoke)
	assertConnectCode(t, err, connect.CodeUnauthenticated)
	api.close(t)

	assertRuntimeDatabaseHashes(t, dataRoot)
	assertRuntimeDataQuiet(t, dataRoot, registrationKey, first.GetToken(), second.GetToken(), renewed.GetToken())
}

func TestAgentRuntimeHTTPConcurrencyBindingAndAuthorityBoundaries(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	client := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)

	const count = 12
	tokens := make([]string, count)
	errorsByIndex := make([]error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := client.CreateAgentRuntimeSession(context.Background(), connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
				ComputerId: computer.GetId(), RegistrationKey: registrationKey,
				AgentId: agent.GetId(), PlacementGeneration: placement.GetGeneration(),
			}))
			if err == nil {
				tokens[index] = response.Msg.GetSession().GetToken()
			}
			errorsByIndex[index] = err
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("create %d: %v", index, err)
		}
	}
	currentToken := ""
	valid := 0
	for _, token := range tokens {
		if _, err := api.app.store.AuthenticateAgentRuntimeSession(context.Background(), token, time.Now()); err == nil {
			valid++
			currentToken = token
		} else if !errors.Is(err, store.ErrAgentRuntimeUnauthenticated) {
			t.Fatal(err)
		}
	}
	if valid != 1 {
		t.Fatalf("valid concurrent tokens = %d, want 1", valid)
	}

	wrongKey := connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: computer.GetId(), RegistrationKey: "wrong-registration-key",
		AgentId: uuid.NewString(), PlacementGeneration: 99,
	})
	_, err := client.CreateAgentRuntimeSession(context.Background(), wrongKey)
	assertConnectCode(t, err, connect.CodePermissionDenied)
	if _, err := api.app.store.AuthenticateAgentRuntimeSession(context.Background(), currentToken, time.Now()); err != nil {
		t.Fatal("wrong-key create changed current runtime session")
	}

	wrongBinding := connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: computer.GetId(), RegistrationKey: registrationKey,
		AgentId: uuid.NewString(), PlacementGeneration: 99,
	})
	_, err = client.CreateAgentRuntimeSession(context.Background(), wrongBinding)
	assertConnectCode(t, err, connect.CodeFailedPrecondition)
	if _, err := api.app.store.AuthenticateAgentRuntimeSession(context.Background(), currentToken, time.Now()); err != nil {
		t.Fatal("wrong binding changed current runtime session")
	}

	ownerProtected := agentv1connect.NewAgentServiceClient(api.http.Client(), api.http.URL)
	ownerRequest := connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Name: "runtime-cannot-create", Driver: agentv1.Driver_DRIVER_NATIVE,
	})
	ownerRequest.Header().Set("Authorization", "Bearer "+currentToken)
	_, err = ownerProtected.CreateAgent(context.Background(), ownerRequest)
	assertConnectCode(t, err, connect.CodeUnauthenticated)

	collaboration := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL)
	listSpaces := connect.NewRequest(&spacev1.ListSpacesRequest{})
	listSpaces.Header().Set("Authorization", "Bearer "+currentToken)
	_, err = collaboration.ListSpaces(context.Background(), listSpaces)
	assertConnectCode(t, err, connect.CodeUnauthenticated)

	other, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: "other-computer-key", Name: "Other runtime host",
		Os: computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, Arch: computerv1.Architecture_ARCHITECTURE_ARM64,
	}))
	if err != nil {
		t.Fatal(err)
	}
	reassigned, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId: uuid.NewString(), AgentId: agent.GetId(), ComputerId: other.Msg.GetComputer().GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if reassigned.Msg.GetPlacement().GetGeneration() <= placement.GetGeneration() || reassigned.Msg.GetPlacement().GetState() != placementv1.PlacementState_PLACEMENT_STATE_PENDING {
		t.Fatalf("reassigned placement = %v", reassigned.Msg.GetPlacement())
	}
	assertStoreRuntimeRejected(t, api.app.store, currentToken)
	staleRenew := runtimeRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: computer.GetId(), RegistrationKey: registrationKey,
	}, currentToken)
	_, err = client.RenewAgentRuntimeSession(context.Background(), staleRenew)
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

func createActiveRuntimeBinding(t *testing.T, api *factsAPI) (*computerv1.Computer, *agentv1.Agent, *placementv1.AgentPlacement, string) {
	t.Helper()
	registrationKey := "server-runtime-registration-key"
	computer, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: registrationKey, Name: "Runtime host",
		Os: computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, Arch: computerv1.Architecture_ARCHITECTURE_ARM64,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Name: "runtime-" + uuid.NewString()[:8], Driver: agentv1.Driver_DRIVER_NATIVE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	placement, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId: uuid.NewString(), AgentId: agent.Msg.GetAgent().GetId(), ComputerId: computer.Msg.GetComputer().GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	active, err := api.placements.AcknowledgeAgentPlacement(context.Background(), connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId: computer.Msg.GetComputer().GetId(), RegistrationKey: registrationKey,
		AgentId: agent.Msg.GetAgent().GetId(), Generation: placement.Msg.GetPlacement().GetGeneration(),
		Result: placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return computer.Msg.GetComputer(), agent.Msg.GetAgent(), active.Msg.GetPlacement(), registrationKey
}

func createRuntimeOverHTTP(t *testing.T, client runtimev1connect.AgentRuntimeServiceClient, computerID, registrationKey, agentID string, generation uint64) *runtimev1.AgentRuntimeSession {
	t.Helper()
	response, err := client.CreateAgentRuntimeSession(context.Background(), connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: computerID, RegistrationKey: registrationKey,
		AgentId: agentID, PlacementGeneration: generation,
	}))
	if err != nil {
		t.Fatal(err)
	}
	session := response.Msg.GetSession()
	if len(session.GetToken()) != 43 || !session.GetExpiresAt().AsTime().After(time.Now().Add(9*time.Minute)) {
		t.Fatalf("runtime response = %v", session)
	}
	return session
}

func renewRuntimeOverHTTP(t *testing.T, client runtimev1connect.AgentRuntimeServiceClient, token, computerID, registrationKey string) *runtimev1.AgentRuntimeSession {
	t.Helper()
	request := runtimeRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: computerID, RegistrationKey: registrationKey,
	}, token)
	response, err := client.RenewAgentRuntimeSession(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetSession()
}

func runtimeRequest[T any](message *T, token string) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func assertStoreRuntimeRejected(t *testing.T, database *store.Store, token string) {
	t.Helper()
	if _, err := database.AuthenticateAgentRuntimeSession(context.Background(), token, time.Now()); !errors.Is(err, store.ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("runtime authentication error = %v", err)
	}
}

func assertRuntimeDatabaseHashes(t *testing.T, dataRoot string) {
	t.Helper()
	layout, err := home.Ensure(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var rows, shortest, longest int
	if err := database.QueryRow(`
		SELECT count(*), min(length(token_hash)), max(length(token_hash)) FROM agent_runtime_sessions
	`).Scan(&rows, &shortest, &longest); err != nil {
		t.Fatal(err)
	}
	if rows < 3 || shortest != 32 || longest != 32 {
		t.Fatalf("runtime token hashes = rows:%d min:%d max:%d", rows, shortest, longest)
	}
}

func assertRuntimeDataQuiet(t *testing.T, dataRoot string, secrets ...string) {
	t.Helper()
	err := filepath.WalkDir(dataRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range secrets {
			if bytes.Contains(payload, []byte(secret)) {
				return fmt.Errorf("%s contains runtime secret", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
