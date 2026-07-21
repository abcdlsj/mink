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
	defer stopComputer(t, computerB, syscall.SIGTERM)

	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxComputerSeen(t, computerPublic, idA)
	})
	waitForBlackbox(t, 15*time.Second, func() bool {
		return blackboxComputerSeen(t, computerPublic, idB)
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
	t.Helper()
	command := exec.Command(binary, "--server", serverURL, "--data-root", root)
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
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM messages WHERE body = ?`, body).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("visible message count for %q = %d, want %d", body, count, want)
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
