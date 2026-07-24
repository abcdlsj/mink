package computer

import (
	"encoding/base64"
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

var (
	osToProto = map[computerdomain.OperatingSystem]computerv1.OperatingSystem{
		computerdomain.OperatingSystemMacOS:  computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS,
		computerdomain.OperatingSystemLinux:  computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
	}
	archToProto = map[computerdomain.Architecture]computerv1.Architecture{
		computerdomain.ArchitectureARM64: computerv1.Architecture_ARCHITECTURE_ARM64,
		computerdomain.ArchitectureAMD64: computerv1.Architecture_ARCHITECTURE_AMD64,
	}
	stateToProto = map[string]computerv1.CredentialDeliveryState{
		string(computerapp.CredentialDeliveryQueued):     computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_QUEUED,
		string(computerapp.CredentialDeliveryClaimed):    computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_CLAIMED,
		string(computerapp.CredentialDeliverySucceeded):  computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_SUCCEEDED,
		string(computerapp.CredentialDeliveryFailed):     computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_FAILED,
		string(computerapp.CredentialDeliveryExpired):    computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_EXPIRED,
	}
	credKindToProto = map[string]computerv1.CredentialKind{
		"openai":         computerv1.CredentialKind_CREDENTIAL_KIND_OPENAI,
		"anthropic":      computerv1.CredentialKind_CREDENTIAL_KIND_ANTHROPIC,
		"codex_adapter":  computerv1.CredentialKind_CREDENTIAL_KIND_CODEX_ADAPTER,
		"claude_adapter": computerv1.CredentialKind_CREDENTIAL_KIND_CLAUDE_ADAPTER,
	}
	storeToProto = map[string]computerv1.CredentialStore{
		"macos_keychain":         computerv1.CredentialStore_CREDENTIAL_STORE_MACOS_KEYCHAIN,
		"linux_secret_service":   computerv1.CredentialStore_CREDENTIAL_STORE_LINUX_SECRET_SERVICE,
	}
)

func computerToProto(c computerapp.Computer, now time.Time) *computerv1.Computer {
	expiresAt := c.LastSeenAt.Add(connectivityTTL)
	return &computerv1.Computer{
		Id: c.ID, Name: c.Name,
		Os:   osToProto[c.OS],
		Arch: archToProto[c.Arch],
		CreatedAt:           servicesvc.Ts(c.CreatedAt),
		LastSeenAt:          servicesvc.Ts(c.LastSeenAt),
		Online:              now.Before(expiresAt),
		ConnectivityExpiresAt: servicesvc.Ts(expiresAt),
		CapabilityInventory:  capInventoryToProto(c.CapabilityInventory),
	}
}

func capInventoryToProto(v computerdomain.CapabilityInventory) *computerv1.CapabilityInventory {
	msg := &computerv1.CapabilityInventory{
		Revision: v.Revision, DeclaredAt: servicesvc.Ts(v.DeclaredAt),
		Engines:            make([]*computerv1.EngineCapability, 0, len(v.Engines)),
		Sandboxes:          make([]*computerv1.SandboxCapability, 0, len(v.Sandboxes)),
		CredentialDelivery: credDeliveryCapToProto(v.CredentialDelivery),
	}
	for _, e := range v.Engines {
		msg.Engines = append(msg.Engines, engineCapToProto(e))
	}
	for _, s := range v.Sandboxes {
		msg.Sandboxes = append(msg.Sandboxes, sandboxCapToProto(s))
	}
	return msg
}

func engineCapToProto(v computerdomain.EngineCapability) *computerv1.EngineCapability {
	msg := &computerv1.EngineCapability{
		Version: v.Version, ProtocolVersion: v.ProtocolVersion,
		SupportsToolCalls: v.SupportsToolCalls, SupportsCancel: v.SupportsCancel,
		Health: computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE,
	}
	switch v.Kind {
	case computerdomain.EngineBuiltin:
		msg.Engine = agentv1.EngineKind_ENGINE_KIND_BUILTIN
	case computerdomain.EngineCodexAdapter:
		msg.Engine = agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER
	case computerdomain.EngineClaudeAdapter:
		msg.Engine = agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER
	}
	if v.Healthy {
		msg.Health = computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY
	}
	if v.OpenAIResponses {
		msg.ProviderProtocols = append(msg.ProviderProtocols, agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES)
	}
	if v.AnthropicMessages {
		msg.ProviderProtocols = append(msg.ProviderProtocols, agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES)
	}
	return msg
}

func sandboxCapToProto(v computerdomain.SandboxCapability) *computerv1.SandboxCapability {
	if v != computerdomain.TrustedLocalSandboxCapability() {
		return nil
	}
	return &computerv1.SandboxCapability{
		Provider:              computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		Isolation:             computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL,
		WorkspaceAccess:       computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE,
		ProcessControl:        computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP,
		FilesystemIsolation:   computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE,
		NetworkIsolation:      computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE,
		SecretMaterialization: computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT,
		DaemonCrashCleanup:    computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE,
	}
}

func credDeliveryCapToProto(v computerdomain.CredentialDeliveryCapability) *computerv1.CredentialDeliveryCapability {
	msg := &computerv1.CredentialDeliveryCapability{Health: computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE}
	if !v.Healthy {
		return msg
	}
	pk, err := base64.RawURLEncoding.DecodeString(v.PublicKey)
	if err != nil {
		return msg
	}
	msg.Health = computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY
	msg.Algorithm = computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305
	msg.KeyId = v.KeyID
	msg.PublicKey = pk
	msg.Store = storeToProto[v.Store]
	return msg
}

func deliveryToProto(d computerapp.CredentialDelivery) *computerv1.CredentialDelivery {
	return &computerv1.CredentialDelivery{
		Id: d.ID, RequestId: d.RequestID, ComputerId: d.ComputerID, AgentId: d.AgentID,
		CredentialKind: credKindToProto[string(d.CredentialKind)],
		State:          stateToProto[string(d.State)],
		BindingHandle:  d.BindingHandle,
		ErrorCode:      d.ErrorCode,
		SealedCredential: &computerv1.SealedCredential{
			Algorithm:          computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305,
			KeyId:              d.Sealed.KeyID,
			EphemeralPublicKey: append([]byte(nil), d.Sealed.EphemeralPublicKey...),
			Nonce:              append([]byte(nil), d.Sealed.Nonce...),
			Ciphertext:         append([]byte(nil), d.Sealed.Ciphertext...),
		},
		ExpiresAt: servicesvc.Ts(d.ExpiresAt),
		CreatedAt: servicesvc.Ts(d.CreatedAt),
		UpdatedAt: servicesvc.Ts(d.UpdatedAt),
	}
}
