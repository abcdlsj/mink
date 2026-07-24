package computer

import (
	"encoding/base64"
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func computerMessage(computer computerapp.Computer, now time.Time) *computerv1.Computer {
	os := computerv1.OperatingSystem_OPERATING_SYSTEM_UNSPECIFIED
	if computer.OS == computerdomain.OperatingSystemMacOS {
		os = computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS
	} else if computer.OS == computerdomain.OperatingSystemLinux {
		os = computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX
	}
	arch := computerv1.Architecture_ARCHITECTURE_UNSPECIFIED
	if computer.Arch == computerdomain.ArchitectureARM64 {
		arch = computerv1.Architecture_ARCHITECTURE_ARM64
	} else if computer.Arch == computerdomain.ArchitectureAMD64 {
		arch = computerv1.Architecture_ARCHITECTURE_AMD64
	}
	expiresAt := computer.LastSeenAt.Add(connectivityTTL)
	return &computerv1.Computer{
		Id: computer.ID, Name: computer.Name, Os: os, Arch: arch,
		CreatedAt: timestamppb.New(computer.CreatedAt), LastSeenAt: timestamppb.New(computer.LastSeenAt),
		Online: now.Before(expiresAt), ConnectivityExpiresAt: timestamppb.New(expiresAt),
		CapabilityInventory: capabilityInventoryMessage(computer.CapabilityInventory),
	}
}

func capabilityInventoryMessage(value computerdomain.CapabilityInventory) *computerv1.CapabilityInventory {
	message := &computerv1.CapabilityInventory{
		Revision: value.Revision, DeclaredAt: timestamppb.New(value.DeclaredAt),
		Engines:            make([]*computerv1.EngineCapability, 0, len(value.Engines)),
		Sandboxes:          make([]*computerv1.SandboxCapability, 0, len(value.Sandboxes)),
		CredentialDelivery: credentialDeliveryCapabilityMessage(value.CredentialDelivery),
	}
	for _, engine := range value.Engines {
		message.Engines = append(message.Engines, engineCapabilityMessage(engine))
	}
	for _, sandbox := range value.Sandboxes {
		message.Sandboxes = append(message.Sandboxes, sandboxCapabilityMessage(sandbox))
	}
	return message
}

func engineCapabilityMessage(value computerdomain.EngineCapability) *computerv1.EngineCapability {
	message := &computerv1.EngineCapability{
		Version: value.Version, ProtocolVersion: value.ProtocolVersion,
		SupportsToolCalls: value.SupportsToolCalls, SupportsCancel: value.SupportsCancel,
		Health: computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE,
	}
	switch value.Kind {
	case computerdomain.EngineBuiltin:
		message.Engine = agentv1.EngineKind_ENGINE_KIND_BUILTIN
	case computerdomain.EngineCodexAdapter:
		message.Engine = agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER
	case computerdomain.EngineClaudeAdapter:
		message.Engine = agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER
	}
	if value.Healthy {
		message.Health = computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY
	}
	if value.OpenAIResponses {
		message.ProviderProtocols = append(message.ProviderProtocols, agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES)
	}
	if value.AnthropicMessages {
		message.ProviderProtocols = append(message.ProviderProtocols, agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES)
	}
	return message
}

func sandboxCapabilityMessage(value computerdomain.SandboxCapability) *computerv1.SandboxCapability {
	if value != computerdomain.TrustedLocalSandboxCapability() {
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

func credentialDeliveryCapabilityMessage(value computerdomain.CredentialDeliveryCapability) *computerv1.CredentialDeliveryCapability {
	message := &computerv1.CredentialDeliveryCapability{Health: computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE}
	if !value.Healthy {
		return message
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(value.PublicKey)
	if err != nil {
		return message
	}
	message.Health = computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY
	message.Algorithm = computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305
	message.KeyId = value.KeyID
	message.PublicKey = publicKey
	switch value.Store {
	case "macos_keychain":
		message.Store = computerv1.CredentialStore_CREDENTIAL_STORE_MACOS_KEYCHAIN
	case "linux_secret_service":
		message.Store = computerv1.CredentialStore_CREDENTIAL_STORE_LINUX_SECRET_SERVICE
	}
	return message
}

func credentialDeliveryMessage(value computerapp.CredentialDelivery) *computerv1.CredentialDelivery {
	state := computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_UNSPECIFIED
	switch value.State {
	case computerapp.CredentialDeliveryQueued:
		state = computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_QUEUED
	case computerapp.CredentialDeliveryClaimed:
		state = computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_CLAIMED
	case computerapp.CredentialDeliverySucceeded:
		state = computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_SUCCEEDED
	case computerapp.CredentialDeliveryFailed:
		state = computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_FAILED
	case computerapp.CredentialDeliveryExpired:
		state = computerv1.CredentialDeliveryState_CREDENTIAL_DELIVERY_STATE_EXPIRED
	}
	kind := computerv1.CredentialKind_CREDENTIAL_KIND_UNSPECIFIED
	switch value.CredentialKind {
	case "openai":
		kind = computerv1.CredentialKind_CREDENTIAL_KIND_OPENAI
	case "anthropic":
		kind = computerv1.CredentialKind_CREDENTIAL_KIND_ANTHROPIC
	case "codex_adapter":
		kind = computerv1.CredentialKind_CREDENTIAL_KIND_CODEX_ADAPTER
	case "claude_adapter":
		kind = computerv1.CredentialKind_CREDENTIAL_KIND_CLAUDE_ADAPTER
	}
	return &computerv1.CredentialDelivery{
		Id: value.ID, RequestId: value.RequestID, ComputerId: value.ComputerID, AgentId: value.AgentID,
		CredentialKind: kind, State: state, BindingHandle: value.BindingHandle, ErrorCode: value.ErrorCode,
		SealedCredential: &computerv1.SealedCredential{
			Algorithm: computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305,
			KeyId:     value.Sealed.KeyID, EphemeralPublicKey: append([]byte(nil), value.Sealed.EphemeralPublicKey...),
			Nonce: append([]byte(nil), value.Sealed.Nonce...), Ciphertext: append([]byte(nil), value.Sealed.Ciphertext...),
		},
		ExpiresAt: timestamppb.New(value.ExpiresAt), CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt),
	}
}
