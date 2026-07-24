package computer

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
)

func credentialKindName(value computerv1.CredentialKind) (string, bool) {
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

func sealedCredential(value *computerv1.SealedCredential) (computerapp.SealedCredential, error) {
	if value == nil || value.GetAlgorithm() != computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305 ||
		value.GetKeyId() == "" || len(value.GetEphemeralPublicKey()) != 32 || len(value.GetNonce()) != 24 ||
		len(value.GetCiphertext()) < 17 || len(value.GetCiphertext()) > 65552 {
		return computerapp.SealedCredential{}, connect.NewError(connect.CodeInvalidArgument, errors.New("sealed credential is invalid"))
	}
	return computerapp.SealedCredential{
		Algorithm: "x25519_xchacha20_poly1305", KeyID: value.GetKeyId(),
		EphemeralPublicKey: append([]byte(nil), value.GetEphemeralPublicKey()...),
		Nonce:              append([]byte(nil), value.GetNonce()...), Ciphertext: append([]byte(nil), value.GetCiphertext()...),
	}, nil
}

func credentialCompletionValid(handle, errorCode string) bool {
	succeeded := len(handle) >= 16 && len(handle) <= 255 && errorCode == ""
	failed := handle == "" && (errorCode == "key_unavailable" || errorCode == "decrypt_failed" || errorCode == "store_unavailable" || errorCode == "binding_failed")
	return succeeded || failed
}

func pairingTokenValid(token string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("pairing token is invalid"))
	}
	return nil
}

func pairParams(request *computerv1.RegisterComputerRequest, now time.Time) (computerapp.PairCommand, error) {
	requestID := request.GetRequestId()
	if requestID == "" {
		return computerapp.PairCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("request id is required"))
	}
	if err := pairingTokenValid(request.GetPairingToken()); err != nil {
		return computerapp.PairCommand{}, err
	}
	if err := registrationKeyValid(request.GetRegistrationKey()); err != nil {
		return computerapp.PairCommand{}, err
	}
	if err := displayNameValid(request.GetName()); err != nil {
		return computerapp.PairCommand{}, err
	}
	os, ok := operatingSystem(request.GetOs())
	if !ok {
		return computerapp.PairCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("operating system must be macos or linux"))
	}
	arch, ok := architecture(request.GetArch())
	if !ok {
		return computerapp.PairCommand{}, connect.NewError(connect.CodeInvalidArgument, errors.New("architecture must be arm64 or amd64"))
	}
	inventory, err := capabilityInventory(request.GetCapabilityInventory())
	if err != nil {
		return computerapp.PairCommand{}, err
	}
	return computerapp.PairCommand{
		RequestID: requestID, PairingToken: request.GetPairingToken(),
		RegistrationKey: request.GetRegistrationKey(), Name: request.GetName(), OS: os, Arch: arch,
		CapabilityInventory: inventory, Now: now,
	}, nil
}

func registrationKeyValid(key string) error {
	if key == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is required"))
	}
	if len(key) > 256 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is too long"))
	}
	return nil
}

func displayNameValid(name string) error {
	if !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name is invalid"))
	}
	size := utf8.RuneCountInString(name)
	if size < 1 || size > 100 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name must contain 1 to 100 characters"))
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name is invalid"))
		}
	}
	return nil
}

func operatingSystem(value computerv1.OperatingSystem) (computerdomain.OperatingSystem, bool) {
	switch value {
	case computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS:
		return computerdomain.OperatingSystemMacOS, true
	case computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX:
		return computerdomain.OperatingSystemLinux, true
	default:
		return "", false
	}
}

func architecture(value computerv1.Architecture) (computerdomain.Architecture, bool) {
	switch value {
	case computerv1.Architecture_ARCHITECTURE_ARM64:
		return computerdomain.ArchitectureARM64, true
	case computerv1.Architecture_ARCHITECTURE_AMD64:
		return computerdomain.ArchitectureAMD64, true
	default:
		return "", false
	}
}

func capabilityInventory(value *computerv1.CapabilityInventoryDeclaration) (computerdomain.CapabilityInventory, error) {
	if value == nil || value.GetCredentialDelivery() == nil {
		return computerdomain.CapabilityInventory{}, invalidCapabilityInventory()
	}
	inventory := computerdomain.CapabilityInventory{
		Engines:   make([]computerdomain.EngineCapability, 0, len(value.GetEngines())),
		Sandboxes: make([]computerdomain.SandboxCapability, 0, len(value.GetSandboxes())),
	}
	for _, value := range value.GetEngines() {
		engine, err := engineCapability(value)
		if err != nil {
			return computerdomain.CapabilityInventory{}, err
		}
		inventory.Engines = append(inventory.Engines, engine)
	}
	for _, value := range value.GetSandboxes() {
		sandbox, err := sandboxCapability(value)
		if err != nil {
			return computerdomain.CapabilityInventory{}, err
		}
		inventory.Sandboxes = append(inventory.Sandboxes, sandbox)
	}
	credential, err := credentialDeliveryCapability(value.GetCredentialDelivery())
	if err != nil {
		return computerdomain.CapabilityInventory{}, err
	}
	inventory.CredentialDelivery = credential
	if !inventory.ValidDeclaration() {
		return computerdomain.CapabilityInventory{}, invalidCapabilityInventory()
	}
	return inventory, nil
}

func engineCapability(value *computerv1.EngineCapability) (computerdomain.EngineCapability, error) {
	if value == nil {
		return computerdomain.EngineCapability{}, invalidCapabilityInventory()
	}
	engine := computerdomain.EngineCapability{
		Version: value.GetVersion(), ProtocolVersion: value.GetProtocolVersion(),
		SupportsToolCalls: value.GetSupportsToolCalls(), SupportsCancel: value.GetSupportsCancel(),
	}
	switch value.GetEngine() {
	case agentv1.EngineKind_ENGINE_KIND_BUILTIN:
		engine.Kind = computerdomain.EngineBuiltin
	case agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER:
		engine.Kind = computerdomain.EngineCodexAdapter
	case agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER:
		engine.Kind = computerdomain.EngineClaudeAdapter
	default:
		return computerdomain.EngineCapability{}, invalidCapabilityInventory()
	}
	switch value.GetHealth() {
	case computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY:
		engine.Healthy = true
	case computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE:
	default:
		return computerdomain.EngineCapability{}, invalidCapabilityInventory()
	}
	seenProtocols := make(map[agentv1.ProviderProtocol]struct{}, len(value.GetProviderProtocols()))
	for _, protocol := range value.GetProviderProtocols() {
		if _, duplicate := seenProtocols[protocol]; duplicate {
			return computerdomain.EngineCapability{}, invalidCapabilityInventory()
		}
		seenProtocols[protocol] = struct{}{}
		switch protocol {
		case agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES:
			engine.OpenAIResponses = true
		case agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES:
			engine.AnthropicMessages = true
		default:
			return computerdomain.EngineCapability{}, invalidCapabilityInventory()
		}
	}
	if !engine.Valid() {
		return computerdomain.EngineCapability{}, invalidCapabilityInventory()
	}
	return engine, nil
}

func sandboxCapability(value *computerv1.SandboxCapability) (computerdomain.SandboxCapability, error) {
	if value == nil {
		return computerdomain.SandboxCapability{}, invalidCapabilityInventory()
	}
	if value.GetProvider() == computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL &&
		value.GetIsolation() == computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL &&
		value.GetWorkspaceAccess() == computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE &&
		value.GetProcessControl() == computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP &&
		value.GetFilesystemIsolation() == computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE &&
		value.GetNetworkIsolation() == computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE &&
		value.GetSecretMaterialization() == computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT &&
		value.GetDaemonCrashCleanup() == computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE {
		return computerdomain.TrustedLocalSandboxCapability(), nil
	}
	return computerdomain.SandboxCapability{}, invalidCapabilityInventory()
}

func credentialDeliveryCapability(value *computerv1.CredentialDeliveryCapability) (computerdomain.CredentialDeliveryCapability, error) {
	if value == nil {
		return computerdomain.CredentialDeliveryCapability{}, invalidCapabilityInventory()
	}
	if value.GetHealth() == computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE {
		if value.GetAlgorithm() != computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_UNSPECIFIED ||
			value.GetStore() != computerv1.CredentialStore_CREDENTIAL_STORE_UNSPECIFIED || value.GetKeyId() != "" || len(value.GetPublicKey()) != 0 {
			return computerdomain.CredentialDeliveryCapability{}, invalidCapabilityInventory()
		}
		return computerdomain.CredentialDeliveryCapability{}, nil
	}
	if value.GetHealth() != computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY ||
		value.GetAlgorithm() != computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305 || len(value.GetPublicKey()) != 32 {
		return computerdomain.CredentialDeliveryCapability{}, invalidCapabilityInventory()
	}
	store := ""
	switch value.GetStore() {
	case computerv1.CredentialStore_CREDENTIAL_STORE_MACOS_KEYCHAIN:
		store = "macos_keychain"
	case computerv1.CredentialStore_CREDENTIAL_STORE_LINUX_SECRET_SERVICE:
		store = "linux_secret_service"
	default:
		return computerdomain.CredentialDeliveryCapability{}, invalidCapabilityInventory()
	}
	capability := computerdomain.CredentialDeliveryCapability{
		Healthy: true, Algorithm: "x25519_xchacha20_poly1305", Store: store, KeyID: value.GetKeyId(),
		PublicKey: base64.RawURLEncoding.EncodeToString(value.GetPublicKey()),
	}
	if !capability.Valid() {
		return computerdomain.CredentialDeliveryCapability{}, invalidCapabilityInventory()
	}
	return capability, nil
}

func invalidCapabilityInventory() error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New("capability inventory is invalid"))
}
