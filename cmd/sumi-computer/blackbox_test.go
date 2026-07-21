package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/driver"
	"github.com/abcdlsj/sumi/internal/sandbox"
	"github.com/abcdlsj/sumi/internal/sandbox/trustedlocal"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

func TestSumiComputerTwoProcessMigrationBlackbox(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("sumi-computer supports macOS and Linux")
	}
	binary := buildComputerBinary(t)
	serverRoot := t.TempDir()
	app, err := server.New(context.Background(), server.Config{DataRoot: serverRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})

	owner := blackboxOwnerOption(t, serverRoot)
	computerOwner := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL, owner)
	computerPublic := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL)
	agentClient := agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL, owner)
	placementClient := placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL, owner)
	runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(httpServer.Client(), httpServer.URL)
	spaceClient := spacev1connect.NewCollaborationServiceClient(httpServer.Client(), httpServer.URL, owner)
	grantClient := grantv1connect.NewGrantServiceClient(httpServer.Client(), httpServer.URL, owner)
	deliveryClient := deliveryv1connect.NewDeliveryServiceClient(httpServer.Client(), httpServer.URL)
	inboxClient := inboxv1connect.NewInboxServiceClient(httpServer.Client(), httpServer.URL)

	agentResponse, err := agentClient.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Name: "blackbox-agent", Driver: agentv1.Driver_DRIVER_NATIVE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentID := agentResponse.Msg.GetAgent().GetId()
	groupResponse, err := spaceClient.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: "blackbox-migration",
	}))
	if err != nil {
		t.Fatal(err)
	}
	groupID := groupResponse.Msg.GetSpace().GetId()
	if _, err := spaceClient.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: groupID,
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
	})); err != nil {
		t.Fatal(err)
	}
	rootGrantID := blackboxRootGrant(t, grantClient)
	for _, capability := range []grantv1.Capability{
		grantv1.Capability_CAPABILITY_SPACE_READ,
		grantv1.Capability_CAPABILITY_MESSAGE_SEND,
		grantv1.Capability_CAPABILITY_RUN_EXECUTE,
	} {
		scope := &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: groupID}
		if capability == grantv1.Capability_CAPABILITY_RUN_EXECUTE {
			scope = &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agentID}
		}
		if _, err := grantClient.IssueGrant(context.Background(), connect.NewRequest(&grantv1.IssueGrantRequest{
			RequestId: uuid.NewString(), Subject: &grantv1.Principal{Kind: grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
			Capability: capability, Scope: scope, ParentGrantId: rootGrantID,
		})); err != nil {
			t.Fatal(err)
		}
	}

	proxy := blackboxProxy(t, httpServer.URL)
	rootA := t.TempDir()
	rootB := t.TempDir()
	idA, keyA := pairComputerProcess(t, binary, proxy.URL, rootA, computerOwner, "computer-a")
	idB, keyB := pairComputerProcess(t, binary, httpServer.URL, rootB, computerOwner, "computer-b")
	computerA := startComputerDaemon(t, binary, proxy.URL, rootA)
	computerB := startComputerDaemon(t, binary, httpServer.URL, rootB)

	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxComputerSeen(t, computerPublic, idA)
	})
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxComputerSeen(t, computerPublic, idB)
	})
	beforeB := blackboxComputerLastSeen(t, computerPublic, idB)
	stopComputer(t, computerB, syscall.SIGKILL)
	startComputerDaemon(t, binary, httpServer.URL, rootB)
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxComputerLastSeen(t, computerPublic, idB).After(beforeB)
	})

	placement, err := placementClient.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId: uuid.NewString(), AgentId: agentID, ComputerId: idA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	generationA := placement.Msg.GetPlacement().GetGeneration()
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxPlacementActive(t, placementClient, agentID, idA, generationA, keyA)
	})
	oldSession := blackboxRuntimeSession(t, runtimeClient, idA, keyA, agentID, generationA)

	triggerResponse, err := spaceClient.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(),
		Target:    &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: groupID}},
		Body:      "blackbox trigger", MentionedAgentIds: []string{agentID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	item := waitForInboxItem(t, inboxClient, oldSession.Token, triggerResponse.Msg.GetMessage().GetId())
	if _, err := inboxClient.ObserveTarget(context.Background(), blackboxRequest(oldSession.Token, &inboxv1.ObserveTargetRequest{
		Target: item.GetTarget(), Limit: 200,
	})); err != nil {
		t.Fatal(err)
	}
	delivery := waitForDelivery(t, deliveryClient, oldSession.Token, triggerResponse.Msg.GetMessage().GetId())
	accepted, err := deliveryClient.AcceptDelivery(context.Background(), blackboxRequest(oldSession.Token, &deliveryv1.AcceptDeliveryRequest{
		RequestId: uuid.NewString(), DeliveryId: delivery.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := deliveryClient.ClaimRun(context.Background(), blackboxRequest(oldSession.Token, &deliveryv1.ClaimRunRequest{
		RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	oldLaunch := claimed.Msg.GetLaunch()

	beforePartition := blackboxComputerLastSeen(t, computerPublic, idA)
	proxy.Offline.Store(true)
	partitionStarted := time.Now()
	waitForBlackbox(t, 12*time.Second, func() bool {
		return time.Since(partitionStarted) >= 11*time.Second
	})
	if lastSeen := blackboxComputerLastSeen(t, computerPublic, idA); lastSeen.After(beforePartition.Add(8 * time.Second)) {
		t.Fatalf("Computer A heartbeat advanced during partition: before=%v after=%v", beforePartition, lastSeen)
	}
	proxy.Offline.Store(false)
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxComputerLastSeen(t, computerPublic, idA).After(beforePartition)
	})
	proxy.Offline.Store(true)
	stopComputer(t, computerA, syscall.SIGKILL)

	if _, err := placementClient.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId: uuid.NewString(), AgentId: agentID, ComputerId: idB,
	})); err != nil {
		t.Fatal(err)
	}
	generationB := generationA + 1
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxPlacementActive(t, placementClient, agentID, idB, generationB, keyB)
	})
	newSession := blackboxRuntimeSession(t, runtimeClient, idB, keyB, agentID, generationB)
	if _, err := deliveryClient.ListDeliveries(context.Background(), blackboxRequest(oldSession.Token, &deliveryv1.ListDeliveriesRequest{Limit: 50})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("old Computer runtime after SIGKILL and migration = %v, want unauthenticated", err)
	}

	expireBlackboxLaunch(t, serverRoot, oldLaunch.GetId())
	reclaimed, err := deliveryClient.ClaimRun(context.Background(), blackboxRequest(newSession.Token, &deliveryv1.ClaimRunRequest{
		RequestId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.Msg.GetLaunch().GetFence() <= oldLaunch.GetFence() || reclaimed.Msg.GetLaunch().GetHolderComputerId() != idB {
		t.Fatalf("reclaimed launch = %+v, old = %+v", reclaimed.Msg.GetLaunch(), oldLaunch)
	}
	staleCompletion := &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
		LaunchId: oldLaunch.GetId(), Fence: oldLaunch.GetFence(),
		Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "stale blackbox completion",
	}
	if _, err := deliveryClient.CompleteRun(context.Background(), blackboxRequest(newSession.Token, staleCompletion)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("stale launch completion = %v, want failed precondition", err)
	}
	completionRequest := &deliveryv1.CompleteRunRequest{
		RequestId: uuid.NewString(), OutboxEventId: uuid.NewString(), RunId: accepted.Msg.GetRun().GetId(),
		LaunchId: reclaimed.Msg.GetLaunch().GetId(), Fence: reclaimed.Msg.GetLaunch().GetFence(),
		Outcome: deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED, Body: "blackbox completion",
	}
	completed, err := deliveryClient.CompleteRun(context.Background(), blackboxRequest(newSession.Token, completionRequest))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := deliveryClient.CompleteRun(context.Background(), blackboxRequest(newSession.Token, completionRequest))
	if err != nil || !proto.Equal(completed.Msg, replayed.Msg) {
		t.Fatalf("completion replay = %+v, %v", replayed, err)
	}
	if completed.Msg.GetMessage() == nil || completed.Msg.GetRun().GetResultMessageId() != completed.Msg.GetMessage().GetId() {
		t.Fatalf("completion result = %+v", completed.Msg)
	}
	assertBlackboxMessageCount(t, serverRoot, "blackbox completion", 1)
}

func TestSumiComputerExternalDriverCompletesDeliveryBlackbox(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("sumi-computer supports macOS and Linux")
	}
	binary := buildComputerBinary(t)
	externalDriver := buildExternalDriverBinary(t)
	t.Setenv("SUMI_EXTERNAL_DRIVER", "1")
	serverRoot := t.TempDir()
	app, err := server.New(context.Background(), server.Config{DataRoot: serverRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})

	owner := blackboxOwnerOption(t, serverRoot)
	computerOwner := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL, owner)
	computerPublic := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL)
	agentClient := agentv1connect.NewAgentServiceClient(httpServer.Client(), httpServer.URL, owner)
	placementClient := placementv1connect.NewPlacementServiceClient(httpServer.Client(), httpServer.URL, owner)
	spaceClient := spacev1connect.NewCollaborationServiceClient(httpServer.Client(), httpServer.URL, owner)
	grantClient := grantv1connect.NewGrantServiceClient(httpServer.Client(), httpServer.URL, owner)

	agentResponse, err := agentClient.CreateAgent(context.Background(), connect.NewRequest(&agentv1.CreateAgentRequest{
		RequestId: uuid.NewString(), Name: "external-blackbox-agent", Driver: agentv1.Driver_DRIVER_CODEX,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentID := agentResponse.Msg.GetAgent().GetId()
	groupResponse, err := spaceClient.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{
		RequestId: uuid.NewString(), Name: "external-blackbox",
	}))
	if err != nil {
		t.Fatal(err)
	}
	groupID := groupResponse.Msg.GetSpace().GetId()
	if _, err := spaceClient.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: groupID,
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
	})); err != nil {
		t.Fatal(err)
	}
	rootGrantID := blackboxRootGrant(t, grantClient)
	for _, capability := range []grantv1.Capability{
		grantv1.Capability_CAPABILITY_SPACE_READ,
		grantv1.Capability_CAPABILITY_MESSAGE_SEND,
		grantv1.Capability_CAPABILITY_RUN_EXECUTE,
	} {
		scope := &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: groupID}
		if capability == grantv1.Capability_CAPABILITY_RUN_EXECUTE {
			scope = &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agentID}
		}
		if _, err := grantClient.IssueGrant(context.Background(), connect.NewRequest(&grantv1.IssueGrantRequest{
			RequestId: uuid.NewString(), Subject: &grantv1.Principal{Kind: grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID},
			Capability: capability, Scope: scope, ParentGrantId: rootGrantID,
		})); err != nil {
			t.Fatal(err)
		}
	}

	computerRoot := t.TempDir()
	computerID, registrationKey := pairComputerProcess(t, binary, httpServer.URL, computerRoot, computerOwner, "external-computer")
	startComputerDaemonWithArgs(t, binary, httpServer.URL, computerRoot,
		"--external-driver", "codex",
		"--external-executable", externalDriver,
		"--external-host-policy", "trusted local blackbox policy",
		"--external-secret", "SUMI_EXTERNAL_DRIVER=computer.environment:SUMI_EXTERNAL_DRIVER",
		"--external-timeout", "2s",
		"--external-termination-grace", "100ms",
		"--external-output-limit", "4096",
	)
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxComputerSeen(t, computerPublic, computerID)
	})
	placement, err := placementClient.SetAgentPlacement(context.Background(), connect.NewRequest(&placementv1.SetAgentPlacementRequest{
		RequestId: uuid.NewString(), AgentId: agentID, ComputerId: computerID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	generation := placement.Msg.GetPlacement().GetGeneration()
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxPlacementActive(t, placementClient, agentID, computerID, generation, registrationKey)
	})
	trigger, err := spaceClient.SendMessage(context.Background(), connect.NewRequest(&spacev1.SendMessageRequest{
		RequestId: uuid.NewString(), Target: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: groupID}},
		Body: "external blackbox trigger", MentionedAgentIds: []string{agentID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxMessageCount(t, serverRoot, "external blackbox completion") == 1
	})
	assertBlackboxCompletedDelivery(t, serverRoot, trigger.Msg.GetMessage().GetId())
}

func TestExternalDriverFailureMatrixBlackbox(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("trusted-local provider supports macOS and Linux")
	}
	for _, mode := range []string{"duplicate", "event-only", "ordinary", "partial", "stdout-overflow", "stderr-overflow", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			external, command := externalDriverForMode(t, mode)
			if _, err := external.Execute(context.Background(), command, nil); err == nil {
				t.Fatalf("external mode %q produced a Completion", mode)
			}
		})
	}
	t.Run("caller cancel", func(t *testing.T) {
		external, command := externalDriverForMode(t, "timeout")
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := external.Execute(ctx, command, nil)
			result <- err
		}()
		time.Sleep(25 * time.Millisecond)
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("caller cancellation did not reap external process")
		}
	})
	t.Run("oversized result cannot become durable completion", func(t *testing.T) {
		external, command := externalDriverForMode(t, "result-overflow")
		result, err := external.Execute(context.Background(), command, nil)
		if err != nil || computerstate.ValidCompletionPayload(result.Body, result.MentionedAgentIDs) {
			t.Fatalf("oversized external result = %+v, %v", result, err)
		}
	})
}

func externalDriverForMode(t *testing.T, mode string) (driver.External, driver.Command) {
	t.Helper()
	binary := buildExternalDriverBinary(t)
	provider, err := trustedlocal.New(trustedlocal.Config{
		ScratchRoot: t.TempDir(), GracePeriod: 20 * time.Millisecond,
		SecretLookup: func(key string) (string, bool) { return "1", key == "EXTERNAL_DRIVER" },
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	input := driver.RunInput{
		AgentID: uuid.NewString(), ComputerID: uuid.NewString(), DeliveryID: uuid.NewString(), RunID: uuid.NewString(), LaunchID: uuid.NewString(),
		Fence: 1, Generation: 1, Workspace: workspace, Capabilities: driver.Capability{Streaming: true, Tools: true, Cancel: true},
		Target: driver.Target{SpaceID: uuid.NewString(), HeadSequence: 1}, CurrentInput: "external-mode:" + mode, HostPolicy: "blackbox",
	}
	outputLimit := int64(4096)
	timeout := 100 * time.Millisecond
	if mode == "result-overflow" {
		outputLimit = 1 << 20
		timeout = 5 * time.Second
	}
	return driver.External{Kind: driver.KindCodex, Runner: driver.ProcessRunner{
		Path: binary, Provider: provider, Timeout: timeout, TerminationGrace: 20 * time.Millisecond, MaxOutputBytes: outputLimit,
		Secrets: []sandbox.SecretEnvironmentVariable{{Name: "SUMI_EXTERNAL_DRIVER", Ref: sandbox.SecretRef{Source: trustedlocal.SecretSourceComputerEnvironment, Key: "EXTERNAL_DRIVER"}}},
	}}, driver.Command{Kind: driver.CommandPrompt, Input: &input}
}

type blackboxProxyState struct {
	Offline atomic.Bool
}

func blackboxProxy(t *testing.T, target string) *blackboxProxyStateServer {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	state := &blackboxProxyState{}
	proxy := httputil.NewSingleHostReverseProxy(parsed)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state.Offline.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return &blackboxProxyStateServer{blackboxProxyState: state, URL: server.URL}
}

type blackboxProxyStateServer struct {
	*blackboxProxyState
	URL string
}

type blackboxComputerProcess struct {
	command *exec.Cmd
	done    chan error
}

func buildComputerBinary(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve blackbox test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "sumi-computer")
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Dir = filepath.Join(root, "cmd/sumi-computer")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sumi-computer: %v\n%s", err, output)
	}
	return binary
}

func buildExternalDriverBinary(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve blackbox test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "external-driver")
	command := exec.Command("go", "build", "-o", binary, "./cmd/sumi-computer/testdata/external_driver")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build external driver: %v\n%s", err, output)
	}
	return binary
}

func pairComputerProcess(t *testing.T, binary, serverURL, root string, owner computerv1connect.ComputerServiceClient, name string) (string, string) {
	t.Helper()
	token := blackboxToken(t, name[len(name)-1])
	if _, err := owner.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(root, "pairing.token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "--once", "--server", serverURL, "--data-root", root, "--pairing-token-file", tokenPath, "--name", name)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pair %s: %v\n%s", name, err, output)
	}
	state, err := computerstate.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, found, identityErr := state.Identity(context.Background())
	closeErr := state.Close()
	if identityErr != nil || closeErr != nil || !found {
		t.Fatalf("paired identity %s = %+v, found=%v, err=%v/%v", name, identity, found, identityErr, closeErr)
	}
	return identity.ComputerID, identity.RegistrationKey
}

func startComputerDaemon(t *testing.T, binary, serverURL, root string) *blackboxComputerProcess {
	return startComputerDaemonWithArgs(t, binary, serverURL, root)
}

func startComputerDaemonWithArgs(t *testing.T, binary, serverURL, root string, arguments ...string) *blackboxComputerProcess {
	t.Helper()
	args := append([]string{"--server", serverURL, "--data-root", root}, arguments...)
	command := exec.Command(binary, args...)
	command.Stdout = new(bytes.Buffer)
	command.Stderr = new(bytes.Buffer)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process := &blackboxComputerProcess{command: command, done: make(chan error, 1)}
	go func() { process.done <- command.Wait() }()
	t.Cleanup(func() {
		if command.Process != nil {
			stopComputer(t, process, syscall.SIGTERM)
		}
	})
	return process
}

func stopComputer(t *testing.T, process *blackboxComputerProcess, signal syscall.Signal) {
	t.Helper()
	if process == nil || process.command.Process == nil {
		return
	}
	if err := process.command.Process.Signal(signal); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	select {
	case <-process.done:
	case <-time.After(10 * time.Second):
		_ = process.command.Process.Kill()
		<-process.done
		t.Fatal("sumi-computer process did not stop")
	}
	process.command.Process = nil
}

func blackboxOwnerOption(t *testing.T, dataRoot string) connect.ClientOption {
	t.Helper()
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+credential)
			return next(ctx, request)
		}
	}))
}

func blackboxRootGrant(t *testing.T, client grantv1connect.GrantServiceClient) string {
	t.Helper()
	response, err := client.ListGrants(context.Background(), connect.NewRequest(&grantv1.ListGrantsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range response.Msg.GetGrants() {
		if grant.GetCapability() == grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN && grant.GetParentGrantId() == "" {
			return grant.GetId()
		}
	}
	t.Fatal("root grant not found")
	return ""
}

func blackboxRuntimeSession(t *testing.T, client runtimev1connect.AgentRuntimeServiceClient, computerID, registrationKey, agentID string, generation uint64) *runtimev1.AgentRuntimeSession {
	t.Helper()
	response, err := client.CreateAgentRuntimeSession(context.Background(), connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: computerID, RegistrationKey: registrationKey, AgentId: agentID, PlacementGeneration: generation,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetSession()
}

func blackboxPlacementActive(t *testing.T, client placementv1connect.PlacementServiceClient, agentID, computerID string, generation uint64, registrationKey string) bool {
	response, err := client.ListComputerPlacements(context.Background(), connect.NewRequest(&placementv1.ListComputerPlacementsRequest{
		ComputerId: computerID, RegistrationKey: registrationKey,
	}))
	if err != nil {
		return false
	}
	for _, placement := range response.Msg.GetPlacements() {
		if placement.GetAgentId() == agentID && placement.GetGeneration() == generation && placement.GetState() == placementv1.PlacementState_PLACEMENT_STATE_ACTIVE {
			return true
		}
	}
	return false
}

func blackboxComputerSeen(t *testing.T, client computerv1connect.ComputerServiceClient, computerID string) bool {
	response, err := client.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: computerID}))
	return err == nil && response.Msg.GetComputer().GetLastSeenAt() != nil
}

func blackboxComputerLastSeen(t *testing.T, client computerv1connect.ComputerServiceClient, computerID string) time.Time {
	response, err := client.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: computerID}))
	if err != nil || response.Msg.GetComputer().GetLastSeenAt() == nil {
		return time.Time{}
	}
	return response.Msg.GetComputer().GetLastSeenAt().AsTime()
}

func waitForInboxItem(t *testing.T, client inboxv1connect.InboxServiceClient, token, messageID string) *inboxv1.InboxItem {
	t.Helper()
	var item *inboxv1.InboxItem
	waitForBlackbox(t, 15*time.Second, func() bool {
		response, err := client.ListInboxItems(context.Background(), blackboxRequest(token, &inboxv1.ListInboxItemsRequest{Limit: 200}))
		if err != nil {
			return false
		}
		for _, candidate := range response.Msg.GetItems() {
			if candidate.GetTriggerMessageId() == messageID {
				item = candidate
				return true
			}
		}
		return false
	})
	return item
}

func waitForDelivery(t *testing.T, client deliveryv1connect.DeliveryServiceClient, token, messageID string) *deliveryv1.Delivery {
	t.Helper()
	var delivery *deliveryv1.Delivery
	waitForBlackbox(t, 15*time.Second, func() bool {
		response, err := client.ListDeliveries(context.Background(), blackboxRequest(token, &deliveryv1.ListDeliveriesRequest{Limit: 200}))
		if err != nil {
			return false
		}
		for _, candidate := range response.Msg.GetDeliveries() {
			if candidate.GetTriggerMessageId() == messageID {
				delivery = candidate
				return true
			}
		}
		return false
	})
	return delivery
}

func expireBlackboxLaunch(t *testing.T, dataRoot, launchID string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().Add(-time.Minute).UnixNano()
	if _, err := database.Exec(`UPDATE run_launches SET claimed_at = ?, expires_at = ? WHERE id = ? AND closed_at IS NULL`, now-10_000_000, now, launchID); err != nil {
		t.Fatal(err)
	}
}

func assertBlackboxMessageCount(t *testing.T, dataRoot, body string, want int) {
	t.Helper()
	if count := blackboxMessageCount(t, dataRoot, body); count != want {
		t.Fatalf("visible message count for %q = %d, want %d", body, count, want)
	}
}

func blackboxMessageCount(t *testing.T, dataRoot, body string) int {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM messages WHERE body = ?`, body).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertBlackboxCompletedDelivery(t *testing.T, dataRoot, triggerMessageID string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var deliveryState, runState string
	err = database.QueryRow(`
		SELECT deliveries.state, runs.state
		FROM deliveries JOIN runs ON runs.delivery_id = deliveries.id
		WHERE deliveries.trigger_message_id = ?`, triggerMessageID).Scan(&deliveryState, &runState)
	if err != nil || deliveryState != "completed" || runState != "completed" {
		t.Fatalf("external delivery state = %q/%q, %v", deliveryState, runState, err)
	}
}

func blackboxRequest[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func blackboxToken(t *testing.T, value byte) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func waitForBlackbox(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("blackbox condition did not become ready within %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
