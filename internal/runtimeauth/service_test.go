package runtimeauth

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
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestAgentRuntimeServiceCreateRenewRevoke(t *testing.T) {
	fixture := openRuntimeServiceFixture(t)
	client := fixture.client(t, NewService(fixture.database, Config{Now: func() time.Time { return fixture.now }}))

	created, err := client.CreateAgentRuntimeSession(context.Background(), connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
		AgentId: fixture.agent.ID, PlacementGeneration: fixture.placement.Generation,
	}))
	if err != nil {
		t.Fatal(err)
	}
	first := created.Msg.GetSession()
	if first.GetAgentId() != fixture.agent.ID || first.GetComputerId() != fixture.computer.ID || first.GetPlacementGeneration() != 1 {
		t.Fatalf("created session = %v", first)
	}
	if len(first.GetToken()) != 43 || !first.GetExpiresAt().AsTime().Equal(fixture.now.Add(10*time.Minute)) {
		t.Fatalf("created token/expiry = %q %s", first.GetToken(), first.GetExpiresAt().AsTime())
	}

	renewRequest := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
	})
	renewRequest.Header().Set("Authorization", "Bearer "+first.GetToken())
	renewed, err := client.RenewAgentRuntimeSession(context.Background(), renewRequest)
	if err != nil {
		t.Fatal(err)
	}
	second := renewed.Msg.GetSession()
	if second.GetToken() == first.GetToken() || len(second.GetToken()) != 43 {
		t.Fatalf("renewed token = %q", second.GetToken())
	}

	replay := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
	})
	replay.Header().Set("Authorization", "Bearer "+first.GetToken())
	_, err = client.RenewAgentRuntimeSession(context.Background(), replay)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)
	if strings.Contains(err.Error(), first.GetToken()) {
		t.Fatalf("old token leaked in error: %v", err)
	}

	wrongKey := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: "wrong-registration-key",
	})
	wrongKey.Header().Set("Authorization", "Bearer "+second.GetToken())
	_, err = client.RenewAgentRuntimeSession(context.Background(), wrongKey)
	assertRuntimeCode(t, err, connect.CodePermissionDenied)
	if strings.Contains(err.Error(), wrongKey.Msg.GetRegistrationKey()) || strings.Contains(err.Error(), second.GetToken()) {
		t.Fatalf("credential leaked in error: %v", err)
	}

	revoke := connect.NewRequest(&runtimev1.RevokeAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
	})
	revoke.Header().Set("Authorization", "Bearer "+second.GetToken())
	if _, err := client.RevokeAgentRuntimeSession(context.Background(), revoke); err != nil {
		t.Fatal(err)
	}
	_, err = client.RevokeAgentRuntimeSession(context.Background(), revoke)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)
}

func TestAgentRuntimeInterceptorProtectsOnlyAllowlistedProcedures(t *testing.T) {
	fixture := openRuntimeServiceFixture(t)
	client := fixture.client(t, NewService(fixture.database, Config{Now: func() time.Time { return fixture.now }}))

	created, err := client.CreateAgentRuntimeSession(context.Background(), connect.NewRequest(&runtimev1.CreateAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
		AgentId: fixture.agent.ID, PlacementGeneration: fixture.placement.Generation,
	}))
	if err != nil {
		t.Fatal(err)
	}
	token := created.Msg.GetSession().GetToken()

	missing := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
	})
	_, err = client.RenewAgentRuntimeSession(context.Background(), missing)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)

	human := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
	})
	human.Header().Set("Authorization", "Bearer "+fixture.humanCredential)
	_, err = client.RenewAgentRuntimeSession(context.Background(), human)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)

	duplicate := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
	})
	duplicate.Header().Add("Authorization", "Bearer "+token)
	duplicate.Header().Add("Authorization", "Bearer "+token)
	_, err = client.RenewAgentRuntimeSession(context.Background(), duplicate)
	assertRuntimeCode(t, err, connect.CodeUnauthenticated)
}

func TestAgentRuntimeInterceptorProofFailsClosedAfterReplacement(t *testing.T) {
	fixture := openRuntimeServiceFixture(t)
	firstToken := runtimeAuthTestToken(31)
	if _, err := fixture.database.CreateAgentRuntimeSession(context.Background(), store.CreateAgentRuntimeSessionParams{
		ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
		AgentID: fixture.agent.ID, PlacementGeneration: 1, Token: firstToken,
		Now: fixture.now, ExpiresAt: fixture.now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	consumer := &replacementConsumer{
		t: t, database: fixture.database, computerID: fixture.computer.ID,
		registrationKey: fixture.registrationKey, agentID: fixture.agent.ID, now: fixture.now.Add(time.Second),
	}
	_, handler := runtimev1connect.NewAgentRuntimeServiceHandler(consumer, connect.WithInterceptors(
		newProcedureInterceptor(fixture.database, func() time.Time { return fixture.now.Add(time.Second) }, runtimev1connect.AgentRuntimeServiceRenewAgentRuntimeSessionProcedure),
	))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	client := runtimev1connect.NewAgentRuntimeServiceClient(httpServer.Client(), httpServer.URL)
	request := connect.NewRequest(&runtimev1.RenewAgentRuntimeSessionRequest{
		ComputerId: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
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
		AgentID: c.agentID, PlacementGeneration: 1, Token: runtimeAuthTestToken(32),
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
	registrationKey := "runtime-service-computer-key"
	computer, err := database.RegisterComputer(context.Background(), store.RegisterComputerParams{
		RegistrationKey: registrationKey, Name: "runtime-host", OS: "linux", Arch: "arm64", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreateAgent(context.Background(), store.CreateAgentParams{
		RequestID: uuid.NewString(), Actor: store.Principal{
			Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID,
		},
		Name: "runtime-agent", Driver: "native", Now: now.Add(time.Second),
	})
	if err != nil {
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
		AgentID: agent.ID, Generation: placement.Generation, State: "active", Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &runtimeServiceFixture{
		database: database, computer: computer, agent: agent, placement: placement,
		registrationKey: registrationKey, humanCredential: humanCredential, now: now.Add(4 * time.Second),
	}
}

func (f *runtimeServiceFixture) client(t *testing.T, service runtimev1connect.AgentRuntimeServiceHandler) runtimev1connect.AgentRuntimeServiceClient {
	t.Helper()
	interceptor := newProcedureInterceptor(
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
