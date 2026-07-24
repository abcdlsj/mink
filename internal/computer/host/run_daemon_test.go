package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAuthoritativeTriggerUsesExactRunFacts(t *testing.T) {
	agentID := uuid.NewString()
	spaceID := uuid.NewString()
	messageID := uuid.NewString()
	target := &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: spaceID}}
	run := &runv1.Run{
		Id: messageID, AgentId: agentID, InboxItemId: uuid.NewString(), TriggerMessageId: messageID,
		SpaceId: spaceID, Target: target, TriggerTargetSequence: 4,
		State: runv1.RunState_RUN_STATE_QUEUED, CreatedAt: timestamppb.Now(),
	}
	observed := &inboxv1.ObserveTargetResponse{
		Target: target, HeadSequence: 5,
		Messages: []*spacev1.Message{{
			Id: messageID, SpaceId: spaceID, TargetSequence: 4,
			Author: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: uuid.NewString()}, Body: "exact trigger",
		}},
	}
	trigger, err := authoritativeTrigger(run, observed)
	if err != nil || trigger.spaceID != spaceID || trigger.observedHead != 5 || trigger.body != "exact trigger" || len(trigger.messages) != 1 {
		t.Fatalf("trigger = %+v, %v", trigger, err)
	}
	observed.Messages[0].TargetSequence = 3
	if _, err := authoritativeTrigger(run, observed); err == nil {
		t.Fatal("mismatched trigger sequence was accepted")
	}
}

func TestRunWorkerJournalsAndDurablyQueuesCompletion(t *testing.T) {
	state, err := computerstate.Open(filepath.Join(t.TempDir(), "computer"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Now().UTC()
	agentID := uuid.NewString()
	computerID := uuid.NewString()
	factory := &runtimeTestFactory{completion: computerruntime.Completion{
		Outcome: computerruntime.OutcomeSucceeded, Body: "completed",
		Usage: computerruntime.Usage{InputUnits: 3, OutputUnits: 2},
	}}
	supervisor, err := computerruntime.NewSupervisor(factory)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(runtimeSlot(agentID, 1, t.TempDir())); err != nil {
		t.Fatal(err)
	}
	daemon := NewDaemon(DaemonConfig{
		State: state, RuntimeSupervisor: supervisor, Now: func() time.Time { return now },
		RunRenewInterval: time.Hour, OutboxInterval: time.Millisecond,
	})
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: computerID, PlacementDesiredRevision: 1,
		Token: runtimeTestToken(), ExpiresAt: now.Add(time.Hour), UpdatedAt: now,
	}
	run := runningRun(agentID, computerID, 1, now)
	daemon.runWorker(context.Background(), session, run, triggerContext{
		spaceID: run.GetSpaceId(), observedHead: run.GetInputBasisTargetSequence(), body: "do it",
	})
	events, err := state.PendingOutbox(context.Background(), 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("pending outbox = %+v, %v", events, err)
	}
	event := events[0]
	if event.RunID != run.GetId() || event.Attempt != run.GetAttempt() || event.Fence != run.GetFence() ||
		event.Body != "completed" || event.UsageInputUnits != 3 || event.UsageOutputUnits != 2 {
		t.Fatalf("outbox event = %+v", event)
	}
	journals, err := state.RunJournals(context.Background(), agentID)
	if err != nil || len(journals) != 1 || journals[0].State != "completed" {
		t.Fatalf("run journals = %+v, %v", journals, err)
	}
}

func TestOutboxReplayRequiresCanonicalRunResponseBeforeAck(t *testing.T) {
	state, err := computerstate.Open(filepath.Join(t.TempDir(), "computer"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Now().UTC()
	agentID := uuid.NewString()
	computerID := uuid.NewString()
	session := computerstate.RuntimeSession{
		AgentID: agentID, ComputerID: computerID, PlacementDesiredRevision: 2,
		Token: runtimeTestToken(), ExpiresAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := state.SaveRuntimeSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	event := computerstate.OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID,
		PlacementDesiredRevision: 2, RunID: uuid.NewString(), Attempt: 3, Fence: 9,
		Outcome: computerstate.CompletionSucceeded, Body: "durable result", CreatedAt: now,
	}
	if err := state.EnqueueOutbox(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	client := &runtimeTestRunClient{complete: func(request *runv1.CompleteRunRequest) (*runv1.CompleteRunResponse, error) {
		messageID := uuid.NewString()
		completedAt := timestamppb.New(now.Add(time.Second))
		return &runv1.CompleteRunResponse{
			Run: &runv1.Run{
				Id: event.RunID, AgentId: agentID, Attempt: event.Attempt, Fence: event.Fence,
				PlacementDesiredRevision: event.PlacementDesiredRevision,
				State:                    runv1.RunState_RUN_STATE_SUCCEEDED, ResultRef: &runv1.Run_ResultMessageId{ResultMessageId: messageID},
				Usage: &runv1.RunUsage{}, CompletedAt: completedAt,
			},
			Result:      &runv1.CompleteRunResponse_Message{Message: &spacev1.Message{Id: messageID}},
			CommittedAt: completedAt,
		}, nil
	}}
	daemon := NewDaemon(DaemonConfig{State: state, Now: func() time.Time { return now }, RPCDeadline: time.Second})
	daemon.runs = client
	if err := daemon.dispatchOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending, err := state.PendingOutbox(context.Background(), 10); err != nil || len(pending) != 0 {
		t.Fatalf("pending after ack = %+v, %v", pending, err)
	}
	if client.completed != 1 {
		t.Fatalf("complete calls = %d", client.completed)
	}
}

func runtimeSlot(agentID string, revision uint64, root string) computerruntime.SlotConfig {
	return computerruntime.SlotConfig{
		AgentID: agentID, ComputerID: "computer-test", PlacementDesiredRevision: revision,
		AgentProfile: &agentv1.AgentProfile{AgentId: agentID, Revision: revision, DisplayName: agentID, Role: "worker", Mission: "test"},
		RuntimeSpec: &agentv1.AgentRuntimeSpec{
			AgentId: agentID, Revision: 1, Engine: agentv1.EngineKind_ENGINE_KIND_BUILTIN,
		},
		Workspace: filepath.Join(root, "workspace"), Home: filepath.Join(root, "home"),
		Temp: filepath.Join(root, "temp"), Cache: filepath.Join(root, "cache"),
	}
}

func runningRun(agentID, computerID string, revision uint64, now time.Time) *runv1.Run {
	return &runv1.Run{
		Id: uuid.NewString(), AgentId: agentID, InboxItemId: uuid.NewString(), TriggerMessageId: uuid.NewString(),
		SpaceId: uuid.NewString(), Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: uuid.NewString()}},
		TriggerTargetSequence: 1, InputBasisTargetSequence: 1, State: runv1.RunState_RUN_STATE_RUNNING,
		Attempt: 1, LeaseHolderComputerId: computerID, LeaseExpiresAt: timestamppb.New(now.Add(time.Minute)),
		Fence: 1, PlacementDesiredRevision: revision, CreatedAt: timestamppb.New(now), StartedAt: timestamppb.New(now),
	}
}

func runtimeTestToken() string {
	return testRuntimeToken(1)
}

func testRuntimeToken(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

type runtimeTestFactory struct {
	completion computerruntime.Completion
}

func (factory *runtimeTestFactory) Validate(computerruntime.SlotConfig) error { return nil }

func (factory *runtimeTestFactory) Open(context.Context, computerruntime.SlotConfig) (computerruntime.Engine, error) {
	return runtimeTestEngine{completion: factory.completion}, nil
}

type runtimeTestEngine struct {
	completion computerruntime.Completion
}

func (engine runtimeTestEngine) Execute(context.Context, computerruntime.Execution) (computerruntime.Completion, error) {
	return engine.completion, nil
}

func (runtimeTestEngine) Close() error { return nil }

type runtimeTestRunClient struct {
	complete  func(*runv1.CompleteRunRequest) (*runv1.CompleteRunResponse, error)
	completed int
}

func (*runtimeTestRunClient) ListRuns(context.Context, *connect.Request[runv1.ListRunsRequest]) (*connect.Response[runv1.ListRunsResponse], error) {
	return nil, errors.New("unexpected ListRuns")
}

func (*runtimeTestRunClient) GetRun(context.Context, *connect.Request[runv1.GetRunRequest]) (*connect.Response[runv1.GetRunResponse], error) {
	return nil, errors.New("unexpected GetRun")
}

func (*runtimeTestRunClient) ClaimRun(context.Context, *connect.Request[runv1.ClaimRunRequest]) (*connect.Response[runv1.ClaimRunResponse], error) {
	return nil, errors.New("unexpected ClaimRun")
}

func (*runtimeTestRunClient) RenewRun(context.Context, *connect.Request[runv1.RenewRunRequest]) (*connect.Response[runv1.RenewRunResponse], error) {
	return nil, errors.New("unexpected RenewRun")
}

func (*runtimeTestRunClient) CancelRun(context.Context, *connect.Request[runv1.CancelRunRequest]) (*connect.Response[runv1.CancelRunResponse], error) {
	return nil, errors.New("unexpected CancelRun")
}

func (client *runtimeTestRunClient) CompleteRun(_ context.Context, request *connect.Request[runv1.CompleteRunRequest]) (*connect.Response[runv1.CompleteRunResponse], error) {
	client.completed++
	response, err := client.complete(request.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}
