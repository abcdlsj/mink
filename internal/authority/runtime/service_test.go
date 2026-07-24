package runtime

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestAgentRuntimeServiceCreateRenewRevoke(t *testing.T) {
	f := openRuntimeServiceFixture(t)
	client := f.client(t, NewService(f.database, Config{Now: func() time.Time { return f.now }}))

	created, err := client.CreateAgentRuntimeSession(context.Background(), connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
		AgentId: f.agent.ID, PlacementDesiredRevision: f.placement.DesiredRevision,
	}))
	if err != nil {
		t.Fatal(err)
	}
	first := created.Msg.GetSession()
	if first.GetAgentId() != f.agent.ID || first.GetComputerId() != f.computer.ID || first.GetPlacementDesiredRevision() != 1 {
		t.Fatalf("created binding = agent:%q computer:%q desired_revision:%d", first.GetAgentId(), first.GetComputerId(), first.GetPlacementDesiredRevision())
	}
	if len(first.GetToken()) != 43 || !first.GetExpiresAt().AsTime().Equal(f.now.Add(10*time.Minute)) {
		t.Fatalf("created token_length:%d expires:%s", len(first.GetToken()), first.GetExpiresAt().AsTime())
	}

	renewRequest := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
	})
	renewRequest.Header().Set("Authorization", "Bearer "+first.GetToken())
	renewed, err := client.RenewAgentRuntimeSession(context.Background(), renewRequest)
	if err != nil {
		t.Fatal(err)
	}
	second := renewed.Msg.GetSession()
	if second.GetToken() == first.GetToken() || len(second.GetToken()) != 43 {
		t.Fatalf("renewed token reused:%t token_length:%d", second.GetToken() == first.GetToken(), len(second.GetToken()))
	}

	replay := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
	})
	replay.Header().Set("Authorization", "Bearer "+first.GetToken())
	_, err = client.RenewAgentRuntimeSession(context.Background(), replay)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)
	if strings.Contains(err.Error(), first.GetToken()) {
		t.Fatal("old token leaked in error")
	}

	wrongKey := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: "wrong-registration-key",
	})
	wrongKey.Header().Set("Authorization", "Bearer "+second.GetToken())
	_, err = client.RenewAgentRuntimeSession(context.Background(), wrongKey)
	assertRuntimeCode(t, err, connect.CodePermissionDenied)
	if strings.Contains(err.Error(), wrongKey.Msg.GetRegistrationKey()) || strings.Contains(err.Error(), second.GetToken()) {
		t.Fatal("credential leaked in error")
	}

	revoke := connect.NewRequest(&runtimev1.RevokeAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
	})
	revoke.Header().Set("Authorization", "Bearer "+second.GetToken())
	if _, err := client.RevokeAgentRuntimeSession(context.Background(), revoke); err != nil {
		t.Fatal(err)
	}
	_, err = client.RevokeAgentRuntimeSession(context.Background(), revoke)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)
}

func TestAgentRuntimeInterceptorProtectsOnlyAllowlistedProcedures(t *testing.T) {
	f := openRuntimeServiceFixture(t)
	client := f.client(t, NewService(f.database, Config{Now: func() time.Time { return f.now }}))

	created, err := client.CreateAgentRuntimeSession(context.Background(), connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
		AgentId: f.agent.ID, PlacementDesiredRevision: f.placement.DesiredRevision,
	}))
	if err != nil {
		t.Fatal(err)
	}
	token := created.Msg.GetSession().GetToken()

	missing := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
	})
	_, err = client.RenewAgentRuntimeSession(context.Background(), missing)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)

	human := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
	})
	human.Header().Set("Authorization", "Bearer "+f.humanCredential)
	_, err = client.RenewAgentRuntimeSession(context.Background(), human)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)

	duplicate := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
	})
	duplicate.Header().Add("Authorization", "Bearer "+token)
	duplicate.Header().Add("Authorization", "Bearer "+token)
	_, err = client.RenewAgentRuntimeSession(context.Background(), duplicate)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)
}

func TestAgentRuntimeInterceptorProofFailsClosedAfterReplacement(t *testing.T) {
	f := openRuntimeServiceFixture(t)
	firstToken := runtimeAuthTestToken(31)
	if _, err := f.database.CreateAgentRuntimeSession(context.Background(), store.CreateAgentRuntimeSessionParams{
		ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
		AgentID: f.agent.ID, PlacementDesiredRevision: 1, Token: firstToken,
		Now: f.now, ExpiresAt: f.now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	consumer := &replacementConsumer{
		t: t, database: f.database, computerID: f.computer.ID,
		registrationKey: f.registrationKey, agentID: f.agent.ID, now: f.now.Add(time.Second),
	}
	_, handler := runtimev1connect.NewAgentRuntimeServiceHandler(consumer, connect.WithInterceptors(
		newProcInterceptor(f.database, func() time.Time { return f.now.Add(time.Second) }, runtimev1connect.AgentRuntimeServiceRenewAgentRuntimeSessionProcedure),
	))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	client := runtimev1connect.NewAgentRuntimeServiceClient(httpServer.Client(), httpServer.URL)
	request := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: f.computer.ID, RegistrationKey: f.registrationKey,
	})
	request.Header().Set("Authorization", "Bearer "+firstToken)
	if _, err := client.RenewAgentRuntimeSession(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if !consumer.recheckRejected {
		t.Fatal("replacement after interceptor did not fail transaction recheck")
	}
}

type replacementConsumer struct {
	runtimev1connect.UnimplementedAgentRuntimeServiceHandler
	t               *testing.T
	database        *store.Store
	computerID      string
	registrationKey string
	agentID         string
	now             time.Time
	recheckRejected bool
}

func (c *replacementConsumer) RenewAgentRuntimeSession(ctx context.Context, _ *connect.Request[runtimev1.RenewAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.RenewAgentRuntimeSessionResponse], error) {
	principal, proof, err := Subject(ctx)
	if err != nil {
		return nil, err
	}
	if principal.Kind != "agent" || principal.ID != c.agentID {
		c.t.Fatalf("subject = %+v", principal)
	}
	if _, err := c.database.CreateAgentRuntimeSession(ctx, store.CreateAgentRuntimeSessionParams{
		ComputerID: c.computerID, RegistrationKey: c.registrationKey,
		AgentID: c.agentID, PlacementDesiredRevision: 1, Token: runtimeAuthTestToken(32),
		Now: c.now, ExpiresAt: c.now.Add(10 * time.Minute),
	}); err != nil {
		c.t.Fatal(err)
	}
	_, err = c.database.RenewAgentRuntimeSession(ctx, store.RenewAgentRuntimeSessionParams{
		Proof: proof, ComputerID: c.computerID, RegistrationKey: c.registrationKey,
		Token: runtimeAuthTestToken(33), Now: c.now.Add(time.Second), ExpiresAt: c.now.Add(10*time.Minute + time.Second),
	})
	c.recheckRejected = errors.Is(err, store.ErrAgentRuntimeUnauthenticated)
	return connect.NewResponse(&runtimev1.RenewAgentRuntimeSessionResponse{}), nil
}

type runtimeServiceFixture struct {
	database        *store.Store
	computer        store.Computer
	agent           store.Agent
	placement       store.AgentPlacement
	registrationKey string
	humanCredential string
	now             time.Time
}

func openRuntimeServiceFixture(t *testing.T) *runtimeServiceFixture {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Date(2026, time.July, 21, 1, 0, 0, 0, time.UTC)
	humanCredential := runtimeAuthTestToken(200)
	bootstrap, err := database.EnsureAuthority(context.Background(), humanCredential, now)
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	registrationKey := "runtime-service-computer-key"
	pairingToken := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := database.CreateComputerPairing(context.Background(), store.CreateComputerPairingParams{
		RequestID: uuid.NewString(), Actor: owner, Token: pairingToken, ExpiresAt: now.Add(time.Minute), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	inventory := computerdomain.TrustedLocalCapabilityInventory(computerdomain.EngineCapability{
		Kind: computerdomain.EngineBuiltin, Version: "test", ProtocolVersion: 1,
		SupportsToolCalls: true, SupportsCancel: true, OpenAIResponses: true, Healthy: true,
	})
	inventory.CredentialDelivery = computerdomain.CredentialDeliveryCapability{
		Healthy: true, Algorithm: "x25519_xchacha20_poly1305", Store: "linux_secret_service",
		KeyID: "runtime-service-key", PublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	}
	computer, err := database.PairComputer(context.Background(), store.PairComputerParams{
		RequestID: uuid.NewString(), PairingToken: pairingToken,
		RegistrationKey: registrationKey, Name: "runtime-host", OS: "linux", Arch: "arm64",
		CapabilityInventory: inventory, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreateAgent(context.Background(), store.CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner,
		Handle: "runtime-agent", DisplayName: "Runtime Agent", Role: "worker", Mission: "Exercise runtime identity", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingHandle := completeRuntimeServiceCredential(t, database, owner, computer.ID, registrationKey, agent.ID, now.Add(time.Second))
	if _, err := database.UpdateAgentRuntimeSpec(context.Background(), store.UpdateAgentRuntimeSpecParams{
		RequestID: uuid.NewString(), Actor: owner,
		AgentID: agent.ID, Engine: agentapp.EngineBuiltin, ProviderProtocol: agentapp.ProviderOpenAIResponses,
		ProviderEndpoint: "https://provider.invalid/v1", Model: "test-model", CredentialBindingHandle: bindingHandle,
		SandboxProvider: "trusted_local", MaxRunDuration: 2 * time.Minute, MaxOutputBytes: 1 << 20,
		ToolPolicy: agentapp.ToolPolicy{Message: true}, Now: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	placement, err := database.SetAgentPlacement(context.Background(), store.SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: store.Principal{
			Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID,
		},
		AgentID: agent.ID, ComputerID: computer.ID, Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	placement, err = database.AcknowledgeAgentPlacement(context.Background(), store.AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey,
		AgentID: agent.ID, DesiredRevision: placement.DesiredRevision, State: "ready", Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &runtimeServiceFixture{
		database: database, computer: computer, agent: agent, placement: placement,
		registrationKey: registrationKey, humanCredential: humanCredential, now: now.Add(4 * time.Second),
	}
}

func completeRuntimeServiceCredential(
	t *testing.T,
	database *store.Store,
	owner store.Principal,
	computerID, registrationKey, agentID string,
	now time.Time,
) string {
	t.Helper()
	delivery, err := database.EnqueueCredentialDelivery(context.Background(), computerapp.EnqueueCredentialDeliveryCommand{
		RequestID: uuid.NewString(), Actor: owner, ComputerID: computerID, AgentID: agentID, CredentialKind: "openai",
		Sealed: computerapp.SealedCredential{
			Algorithm: "x25519_xchacha20_poly1305", KeyID: "runtime-service-key",
			EphemeralPublicKey: make([]byte, 32), Nonce: make([]byte, 24), Ciphertext: make([]byte, 17),
		},
		ExpiresAt: now.Add(5 * time.Minute), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := database.ClaimCredentialDelivery(context.Background(), computerapp.ClaimCredentialDeliveryCommand{
		ComputerID: computerID, RegistrationKey: registrationKey, Now: now,
	})
	if err != nil || claimed.ID != delivery.ID {
		t.Fatalf("credential claim = %+v, %v", claimed, err)
	}
	handle := "cred_runtime_service_" + agentID
	completed, err := database.CompleteCredentialDelivery(context.Background(), computerapp.CompleteCredentialDeliveryCommand{
		ComputerID: computerID, RegistrationKey: registrationKey, DeliveryID: delivery.ID, BindingHandle: handle, Now: now,
	})
	if err != nil || completed.State != computerapp.CredentialDeliverySucceeded {
		t.Fatalf("credential completion = %+v, %v", completed, err)
	}
	return handle
}

func (f *runtimeServiceFixture) client(t *testing.T, service runtimev1connect.AgentRuntimeServiceHandler) runtimev1connect.AgentRuntimeServiceClient {
	t.Helper()
	interceptor := newProcInterceptor(
		f.database,
		func() time.Time { return f.now },
		runtimev1connect.AgentRuntimeServiceRenewAgentRuntimeSessionProcedure,
		runtimev1connect.AgentRuntimeServiceRevokeAgentRuntimeSessionProcedure,
	)
	_, handler := runtimev1connect.NewAgentRuntimeServiceHandler(service, connect.WithInterceptors(interceptor))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	return runtimev1connect.NewAgentRuntimeServiceClient(httpServer.Client(), httpServer.URL)
}

func runtimeAuthTestToken(value byte) string {
	payload := make([]byte, 32)
	for index := range payload {
		payload[index] = value
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func assertRuntimeCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil || connect.CodeOf(err) != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}
