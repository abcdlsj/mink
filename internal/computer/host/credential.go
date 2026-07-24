package host

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/credential"
)

func (d *Daemon) credentialLoop(ctx context.Context, identity computerstate.Identity) {
	d.periodicLoop(ctx, d.config.SnapshotInterval, d.runtimeLogger, "credential.delivery", func(ctx context.Context) error {
		return d.processCredentialDelivery(ctx, identity)
	})
}

func (d *Daemon) processCredentialDelivery(ctx context.Context, identity computerstate.Identity) error {
	rpcCtx, cancel := d.rpcContext(ctx)
	response, err := d.computers.ClaimCredentialDelivery(rpcCtx, connect.NewRequest(&computerv1.ClaimCredentialDeliveryRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
	}))
	cancel()
	if err != nil {
		return fmt.Errorf("claim credential delivery: %w", err)
	}
	if response == nil || response.Msg.GetDelivery() == nil {
		return nil
	}
	delivery, err := localCredentialDelivery(response.Msg.GetDelivery(), identity.ComputerID)
	if err != nil {
		return err
	}
	handle, errorCode, bindErr := d.config.CredentialManager.Bind(ctx, delivery)
	if bindErr != nil && errorCode == "" {
		errorCode = "binding_failed"
	}
	rpcCtx, cancel = d.rpcContext(ctx)
	completion, completeErr := d.computers.CompleteCredentialDelivery(rpcCtx, connect.NewRequest(&computerv1.CompleteCredentialDeliveryRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey, DeliveryId: delivery.ID,
		BindingHandle: handle, ErrorCode: errorCode,
	}))
	cancel()
	if completeErr != nil {
		return fmt.Errorf("complete credential delivery: %w", completeErr)
	}
	if completion == nil || completion.Msg.GetDelivery() == nil {
		return errors.New("complete credential delivery returned no response")
	}
	if bindErr != nil {
		return fmt.Errorf("bind credential delivery: %w", bindErr)
	}
	if completed := completion.Msg.GetDelivery(); completed.GetState() != computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_SUCCEEDED || completed.GetBindingHandle() != handle {
		return errors.New("credential delivery completion does not match local binding")
	}
	return nil
}

func localCredentialDelivery(value *computerv1.CredentialDelivery, computerID string) (credential.Delivery, error) {
	if value == nil || value.GetComputerId() != computerID || value.GetState() != computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_CLAIMED ||
		value.GetSealedCredential() == nil || value.GetSealedCredential().GetAlgorithm() != computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305 ||
		len(value.GetSealedCredential().GetEphemeralPublicKey()) != 32 || len(value.GetSealedCredential().GetNonce()) != 24 || len(value.GetSealedCredential().GetCiphertext()) < 17 ||
		value.GetExpiresAt() == nil || value.GetExpiresAt().CheckValid() != nil {
		return credential.Delivery{}, errors.New("credential delivery facts are invalid")
	}
	kind, ok := localCredentialKind(value.GetCredentialKind())
	if !ok {
		return credential.Delivery{}, errors.New("credential delivery kind is invalid")
	}
	delivery := credential.Delivery{
		ID: value.GetId(), RequestID: value.GetRequestId(), ComputerID: value.GetComputerId(), AgentID: value.GetAgentId(),
		CredentialKind: kind, KeyID: value.GetSealedCredential().GetKeyId(),
		Ciphertext: append([]byte(nil), value.GetSealedCredential().GetCiphertext()...), ExpiresAt: value.GetExpiresAt().AsTime(),
	}
	copy(delivery.EphemeralPublicKey[:], value.GetSealedCredential().GetEphemeralPublicKey())
	copy(delivery.Nonce[:], value.GetSealedCredential().GetNonce())
	return delivery, nil
}

func localCredentialKind(value computerv1.CredentialKind) (string, bool) {
	switch value {
	case computerv1.CredentialKind_CREDENTIAL_KIND_OPENAI:
		return "openai", true
	case computerv1.CredentialKind_CREDENTIAL_KIND_ANTHROPIC:
		return "anthropic", true
	case computerv1.CredentialKind_CREDENTIAL_KIND_CODEX_ADAPTER:
		return "codex_adapter", true
	case computerv1.CredentialKind_CREDENTIAL_KIND_CLAUDE_ADAPTER:
		return "claude_adapter", true
	default:
		return "", false
	}
}
