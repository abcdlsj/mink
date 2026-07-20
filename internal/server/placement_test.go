package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	"github.com/abcdlsj/sumi/internal/placementcode"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestPlacementSetConcurrencyAndRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	agentID := createPlacementAgent(t, api, "placement-agent")
	computerID := registerPlacementComputer(t, api, "placement-computer-key", "Placement host")
	request := &placementv1.SetAgentPlacementRequest{AgentId: agentID, ComputerId: computerID}

	placements := setPlacementsConcurrently(t, api.placements, request, 20)
	for _, placement := range placements {
		if placement.GetGeneration() != 1 || placement.GetState() != placementv1.PlacementState_PLACEMENT_STATE_PENDING {
			t.Fatalf("concurrent placement = %v", placement)
		}
		if !proto.Equal(placement, placements[0]) {
			t.Fatalf("concurrent Set changed placement: %v != %v", placement, placements[0])
		}
	}
	time.Sleep(time.Millisecond)
	idempotent := setPlacement(t, api, agentID, computerID)
	if !proto.Equal(idempotent, placements[0]) {
		t.Fatalf("idempotent Set changed placement: %v != %v", idempotent, placements[0])
	}
	api.close(t)

	api = openFactsAPI(t, dataRoot)
	persisted := getPlacement(t, api, agentID)
	if !proto.Equal(persisted, placements[0]) {
		t.Fatalf("placement changed across restart: %v != %v", persisted, placements[0])
	}
	secondAgent := createPlacementAgent(t, api, "placement-agent-two")
	setPlacement(t, api, secondAgent, computerID)
	listed, err := api.placements.ListAgentPlacements(context.Background(), connect.NewRequest(&placementv1.ListAgentPlacementsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := placementAgentIDs(listed.Msg.GetPlacements())
	wantIDs := []string{agentID, secondAgent}
	sort.Strings(wantIDs)
	if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("placement order = %v, want %v", gotIDs, wantIDs)
	}
	api.close(t)
}

func TestPlacementAcknowledgementFenceAndConflict(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	agentID := createPlacementAgent(t, api, "fenced-agent")
	firstKey := "first-placement-key"
	secondKey := "second-placement-key"
	firstComputer := registerPlacementComputer(t, api, firstKey, "First host")
	secondComputer := registerPlacementComputer(t, api, secondKey, "Second host")
	first := setPlacement(t, api, agentID, firstComputer)

	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodePermissionDenied, func() error {
		_, err := acknowledgePlacement(api, firstComputer, "wrong-key", agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, "")
		return err
	})
	second := setPlacement(t, api, agentID, secondComputer)
	if second.GetGeneration() != 2 || second.GetState() != placementv1.PlacementState_PLACEMENT_STATE_PENDING || second.GetErrorCode() != "" {
		t.Fatalf("replacement placement = %v", second)
	}
	if !second.GetCreatedAt().AsTime().Equal(first.GetCreatedAt().AsTime()) {
		t.Fatalf("replacement changed created_at from %s to %s", first.GetCreatedAt().AsTime(), second.GetCreatedAt().AsTime())
	}
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeFailedPrecondition, func() error {
		_, err := acknowledgePlacement(api, firstComputer, firstKey, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, "")
		return err
	})
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeFailedPrecondition, func() error {
		_, err := acknowledgePlacement(api, secondComputer, secondKey, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, "")
		return err
	})
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeInvalidArgument, func() error {
		_, err := acknowledgePlacement(api, secondComputer, secondKey, agentID, 2, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED, "free/text/path")
		return err
	})

	active, err := acknowledgePlacement(api, secondComputer, secondKey, agentID, 2, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	repeated, err := acknowledgePlacement(api, secondComputer, secondKey, agentID, 2, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, "")
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(repeated, active) {
		t.Fatalf("duplicate active acknowledgement changed placement: %v != %v", repeated, active)
	}
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeFailedPrecondition, func() error {
		_, err := acknowledgePlacement(api, secondComputer, secondKey, agentID, 2, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED, placementcode.WorkspaceIOError)
		return err
	})
}

func TestFailedPlacementRequiresExplicitRetry(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	agentID := createPlacementAgent(t, api, "failed-agent")
	key := "failed-placement-key"
	computerID := registerPlacementComputer(t, api, key, "Failed host")
	setPlacement(t, api, agentID, computerID)
	failed, err := acknowledgePlacement(api, computerID, key, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED, placementcode.WorkspaceInvalid)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	repeated, err := acknowledgePlacement(api, computerID, key, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED, placementcode.WorkspaceInvalid)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(repeated, failed) {
		t.Fatalf("duplicate failed acknowledgement changed placement: %v != %v", repeated, failed)
	}
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeFailedPrecondition, func() error {
		_, err := acknowledgePlacement(api, computerID, key, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED, placementcode.AgentHomeInvalid)
		return err
	})
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeFailedPrecondition, func() error {
		_, err := acknowledgePlacement(api, computerID, key, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, "")
		return err
	})

	retried := setPlacement(t, api, agentID, computerID)
	if retried.GetGeneration() != 2 || retried.GetState() != placementv1.PlacementState_PLACEMENT_STATE_PENDING || retried.GetErrorCode() != "" {
		t.Fatalf("retried placement = %v", retried)
	}
	if !retried.GetCreatedAt().AsTime().Equal(failed.GetCreatedAt().AsTime()) || !retried.GetUpdatedAt().AsTime().After(failed.GetUpdatedAt().AsTime()) {
		t.Fatalf("retry timestamps = created %s updated %s, failed = created %s updated %s", retried.GetCreatedAt().AsTime(), retried.GetUpdatedAt().AsTime(), failed.GetCreatedAt().AsTime(), failed.GetUpdatedAt().AsTime())
	}
}

func TestComputerAssignmentsArePrivatePendingAndSorted(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	firstKey := "assignments-first-key"
	secondKey := "assignments-second-key"
	firstComputer := registerPlacementComputer(t, api, firstKey, "Assignments one")
	secondComputer := registerPlacementComputer(t, api, secondKey, "Assignments two")
	agentIDs := []string{
		createPlacementAgent(t, api, "assignment-charlie"),
		createPlacementAgent(t, api, "assignment-alpha"),
		createPlacementAgent(t, api, "assignment-bravo"),
	}
	setPlacement(t, api, agentIDs[0], firstComputer)
	setPlacement(t, api, agentIDs[1], firstComputer)
	setPlacement(t, api, agentIDs[2], secondComputer)

	assignments, err := api.placements.ListComputerAssignments(context.Background(), connect.NewRequest(&placementv1.ListComputerAssignmentsRequest{
		ComputerId:      firstComputer,
		RegistrationKey: firstKey,
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := placementAgentIDs(assignments.Msg.GetAssignments())
	want := append([]string(nil), agentIDs[:2]...)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("assignment order = %v, want %v", got, want)
	}
	encoded, err := protojson.Marshal(assignments.Msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), firstKey) || strings.Contains(string(encoded), "registrationKey") {
		t.Fatalf("registration key leaked in assignments: %s", encoded)
	}

	_, err = api.placements.ListComputerAssignments(context.Background(), connect.NewRequest(&placementv1.ListComputerAssignmentsRequest{
		ComputerId:      firstComputer,
		RegistrationKey: "wrong-key",
	}))
	assertConnectCode(t, err, connect.CodePermissionDenied)
	_, err = api.placements.ListComputerAssignments(context.Background(), connect.NewRequest(&placementv1.ListComputerAssignmentsRequest{
		ComputerId:      uuid.NewString(),
		RegistrationKey: firstKey,
	}))
	assertConnectCode(t, err, connect.CodeNotFound)

	if _, err := acknowledgePlacement(api, firstComputer, firstKey, agentIDs[0], 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, ""); err != nil {
		t.Fatal(err)
	}
	assignments, err = api.placements.ListComputerAssignments(context.Background(), connect.NewRequest(&placementv1.ListComputerAssignmentsRequest{
		ComputerId:      firstComputer,
		RegistrationKey: firstKey,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := placementAgentIDs(assignments.Msg.GetAssignments()); len(got) != 1 || got[0] != agentIDs[1] {
		t.Fatalf("assignments after ack = %v", got)
	}
}

func TestPlacementValidationAndNotFound(t *testing.T) {
	api := openFactsAPI(t, t.TempDir())
	defer api.close(t)
	agentID := createPlacementAgent(t, api, "validation-agent")
	key := "validation-placement-key"
	computerID := registerPlacementComputer(t, api, key, "Validation host")
	_, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{AgentId: "not-a-uuid", ComputerId: computerID}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)
	_, err = api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{AgentId: uuid.NewString(), ComputerId: computerID}))
	assertConnectCode(t, err, connect.CodeNotFound)
	_, err = api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{AgentId: agentID, ComputerId: uuid.NewString()}))
	assertConnectCode(t, err, connect.CodeNotFound)
	setPlacement(t, api, agentID, computerID)
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeInvalidArgument, func() error {
		_, err := acknowledgePlacement(api, computerID, key, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_UNSPECIFIED, "")
		return err
	})
	assertRejectedWithoutPlacementMutation(t, api, agentID, connect.CodeInvalidArgument, func() error {
		_, err := acknowledgePlacement(api, computerID, key, agentID, 1, placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE, placementcode.WorkspaceInvalid)
		return err
	})
}

func createPlacementAgent(t *testing.T, api *factsAPI, name string) string {
	t.Helper()
	response, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(),
		Name:      name,
		Driver:    agentv1.Driver_DRIVER_NATIVE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetAgent().GetId()
}

func registerPlacementComputer(t *testing.T, api *factsAPI, key, name string) string {
	t.Helper()
	response, err := api.computers.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: key,
		Name:            name,
		Os:              computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
		Arch:            computerv1.Architecture_ARCHITECTURE_AMD64,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetComputer().GetId()
}

func setPlacement(t *testing.T, api *factsAPI, agentID, computerID string) *placementv1.AgentPlacement {
	t.Helper()
	response, err := api.placements.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{AgentId: agentID, ComputerId: computerID}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetPlacement()
}

func getPlacement(t *testing.T, api *factsAPI, agentID string) *placementv1.AgentPlacement {
	t.Helper()
	response, err := api.placements.GetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.GetAgentPlacementRequest{AgentId: agentID}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetPlacement()
}

func acknowledgePlacement(api *factsAPI, computerID, key, agentID string, generation uint64, result placementv1.AcknowledgementResult, errorCode string) (*placementv1.AgentPlacement, error) {
	response, err := api.placements.AcknowledgeAgentPlacement(context.Background(), connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId:      computerID,
		RegistrationKey: key,
		AgentId:         agentID,
		Generation:      generation,
		Result:          result,
		ErrorCode:       errorCode,
	}))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetPlacement(), nil
}

func assertRejectedWithoutPlacementMutation(t *testing.T, api *factsAPI, agentID string, code connect.Code, action func() error) {
	t.Helper()
	before := getPlacement(t, api, agentID)
	err := action()
	assertConnectCode(t, err, code)
	after := getPlacement(t, api, agentID)
	if !proto.Equal(after, before) {
		t.Fatalf("rejected action changed placement: before %v after %v", before, after)
	}
}

func setPlacementsConcurrently(t *testing.T, client placementv1connect.PlacementServiceClient, request *placementv1.SetAgentPlacementRequest, count int) []*placementv1.AgentPlacement {
	t.Helper()
	placements := make([]*placementv1.AgentPlacement, count)
	errors := make(chan error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := client.SetAgentPlacement(context.Background(), connect.NewRequest(request))
			if err != nil {
				errors <- err
				return
			}
			placements[index] = response.Msg.GetPlacement()
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	return placements
}

func placementAgentIDs(placements []*placementv1.AgentPlacement) []string {
	ids := make([]string, 0, len(placements))
	for _, placement := range placements {
		ids = append(ids, placement.GetAgentId())
	}
	return ids
}
