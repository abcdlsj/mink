package host

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/credential"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCredentialDeliveryReplaysCompletionWithoutStoringSecretTwice(t *testing.T) {
	state, err := computerstate.Open(filepath.Join(t.TempDir(), "computer"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)
	facility := &hostCredentialFacility{secrets: make(map[string][]byte)}
	manager, err := credential.NewManager(context.Background(), state, facility, rand.Reader, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	identity := computerstate.Identity{ComputerID: uuid.NewString(), RegistrationKey: "credential-daemon-registration-key"}
	delivery := sealedCredentialDelivery(t, manager.Key(), identity.ComputerID, uuid.NewString(), now, []byte("credential-daemon-secret"))
	client := &credentialDaemonClient{
		delivery:       delivery,
		completeErrors: []error{errors.New("completion transport unavailable")},
	}
	daemon := NewDaemon(DaemonConfig{
		State: state, CredentialManager: manager, RPCDeadline: time.Second, Now: func() time.Time { return now },
	})
	daemon.computers = client

	if err := daemon.processCredentialDelivery(context.Background(), identity); err == nil || !strings.Contains(err.Error(), "complete credential delivery") {
		t.Fatalf("first delivery error = %v", err)
	}
	binding, found, err := state.CredentialBindingByDelivery(context.Background(), delivery.GetId())
	if err != nil || !found {
		t.Fatalf("local binding after completion failure = %+v, %v, %v", binding, found, err)
	}
	if facility.puts != 1 || string(facility.secrets[binding.Handle]) != "credential-daemon-secret" {
		t.Fatalf("secure facility after first attempt: puts=%d secret=%q", facility.puts, facility.secrets[binding.Handle])
	}

	if err := daemon.processCredentialDelivery(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if facility.puts != 1 {
		t.Fatalf("delivery replay stored the Secret %d times", facility.puts)
	}
	if len(client.completions) != 2 || client.completions[0].GetBindingHandle() != binding.Handle || client.completions[1].GetBindingHandle() != binding.Handle {
		t.Fatalf("completion requests = %+v", client.completions)
	}

	client.delivery = proto.Clone(delivery).(*computerv1.CredentialDelivery)
	client.delivery.AgentId = uuid.NewString()
	if err := daemon.processCredentialDelivery(context.Background(), identity); err == nil || !strings.Contains(err.Error(), "bind credential delivery") {
		t.Fatalf("conflicting delivery error = %v", err)
	}
	if facility.puts != 1 {
		t.Fatalf("conflicting delivery stored another Secret: puts=%d", facility.puts)
	}
	conflict := client.completions[len(client.completions)-1]
	if conflict.GetBindingHandle() != "" || conflict.GetErrorCode() != "binding_failed" {
		t.Fatalf("conflicting completion = %+v", conflict)
	}
}

func sealedCredentialDelivery(
	t *testing.T,
	key computerstate.CredentialDeliveryKey,
	computerID, agentID string,
	now time.Time,
	plaintext []byte,
) *computerv1.CredentialDelivery {
	t.Helper()
	requestID := uuid.NewString()
	expiresAt := now.Add(5 * time.Minute)
	sealed, err := credential.Seal(rand.Reader, key.PublicKey, credential.DeliveryContext{
		RequestID: requestID, ComputerID: computerID, AgentID: agentID,
		CredentialKind: "openai", KeyID: key.KeyID, ExpiresAt: expiresAt,
	}, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return &computerv1.CredentialDelivery{
		Id: requestID, RequestId: requestID, ComputerId: computerID, AgentId: agentID,
		CredentialKind: computerv1.CredentialKind_CREDENTIAL_KIND_OPENAI,
		SealedCredential: &computerv1.SealedCredential{
			Algorithm: computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305,
			KeyId:     key.KeyID, EphemeralPublicKey: sealed.EphemeralPublicKey[:], Nonce: sealed.Nonce[:], Ciphertext: sealed.Ciphertext,
		},
		State:     computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_CLAIMED,
		ExpiresAt: timestamppb.New(expiresAt), CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
	}
}

type credentialDaemonClient struct {
	delivery       *computerv1.CredentialDelivery
	completeErrors []error
	completions    []*computerv1.CompleteCredentialDeliveryRequest
}

func (client *credentialDaemonClient) HeartbeatComputer(context.Context, *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error) {
	return nil, errors.New("unexpected heartbeat")
}

func (client *credentialDaemonClient) ClaimCredentialDelivery(context.Context, *connect.Request[computerv1.ClaimCredentialDeliveryRequest]) (*connect.Response[computerv1.ClaimCredentialDeliveryResponse], error) {
	return connect.NewResponse(&computerv1.ClaimCredentialDeliveryResponse{Delivery: proto.Clone(client.delivery).(*computerv1.CredentialDelivery)}), nil
}

func (client *credentialDaemonClient) CompleteCredentialDelivery(_ context.Context, request *connect.Request[computerv1.CompleteCredentialDeliveryRequest]) (*connect.Response[computerv1.CompleteCredentialDeliveryResponse], error) {
	message := proto.Clone(request.Msg).(*computerv1.CompleteCredentialDeliveryRequest)
	client.completions = append(client.completions, message)
	if len(client.completeErrors) > 0 {
		err := client.completeErrors[0]
		client.completeErrors = client.completeErrors[1:]
		return nil, err
	}
	delivery := proto.Clone(client.delivery).(*computerv1.CredentialDelivery)
	delivery.BindingHandle = message.GetBindingHandle()
	delivery.ErrorCode = message.GetErrorCode()
	if delivery.GetBindingHandle() != "" {
		delivery.State = computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_SUCCEEDED
	} else {
		delivery.State = computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_FAILED
	}
	return connect.NewResponse(&computerv1.CompleteCredentialDeliveryResponse{Delivery: delivery}), nil
}

type hostCredentialFacility struct {
	secrets map[string][]byte
	puts    int
}

func (facility *hostCredentialFacility) Kind() string { return "test_secure_facility" }

func (facility *hostCredentialFacility) Put(_ context.Context, handle string, secret []byte) error {
	facility.puts++
	facility.secrets[handle] = append([]byte(nil), secret...)
	return nil
}

func (facility *hostCredentialFacility) Get(_ context.Context, handle string) ([]byte, error) {
	secret, found := facility.secrets[handle]
	if !found {
		return nil, errors.New("credential not found")
	}
	return append([]byte(nil), secret...), nil
}

func (facility *hostCredentialFacility) Delete(_ context.Context, handle string) error {
	delete(facility.secrets, handle)
	return nil
}
