package computer

import (
	"encoding/base64"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

var credKindMap = map[computerv1.CredentialKind]string{
	computerv1.CredentialKind_CREDENTIAL_KIND_OPENAI:         "openai",
	computerv1.CredentialKind_CREDENTIAL_KIND_ANTHROPIC:      "anthropic",
	computerv1.CredentialKind_CREDENTIAL_KIND_CODEX_ADAPTER:  "codex_adapter",
	computerv1.CredentialKind_CREDENTIAL_KIND_CLAUDE_ADAPTER: "claude_adapter",
}

func credKindFromProto(kind computerv1.CredentialKind) (string, bool) {
	v, ok := credKindMap[kind]
	return v, ok
}

var osFromProto = map[computerv1.OperatingSystem]computerdomain.OperatingSystem{
	computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS: computerdomain.OperatingSystemMacOS,
	computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX: computerdomain.OperatingSystemLinux,
}

var archFromProto = map[computerv1.Architecture]computerdomain.Architecture{
	computerv1.Architecture_ARCHITECTURE_ARM64: computerdomain.ArchitectureARM64,
	computerv1.Architecture_ARCHITECTURE_AMD64: computerdomain.ArchitectureAMD64,
}

func parseSealedCredential(v *computerv1.SealedCredential) (computerapp.SealedCredential, error) {
	if v == nil ||
		v.GetAlgorithm() != computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305 ||
		v.GetKeyId() == "" || len(v.GetEphemeralPublicKey()) != 32 ||
		len(v.GetNonce()) != 24 || len(v.GetCiphertext()) < 17 || len(v.GetCiphertext()) > 65552 {
		return computerapp.SealedCredential{}, servicesvc.InvalArg("sealed credential is invalid")
	}
	return computerapp.SealedCredential{
		Algorithm: "x25519_xchacha20_poly1305", KeyID: v.GetKeyId(),
		EphemeralPublicKey: append([]byte(nil), v.GetEphemeralPublicKey()...),
		Nonce:              append([]byte(nil), v.GetNonce()...),
		Ciphertext:         append([]byte(nil), v.GetCiphertext()...),
	}, nil
}

func completionValid(handle, errorCode string) bool {
	succeeded := len(handle) >= 16 && len(handle) <= 255 && errorCode == ""
	failed := handle == "" && (errorCode == "key_unavailable" || errorCode == "decrypt_failed" ||
		errorCode == "store_unavailable" || errorCode == "binding_failed")
	return succeeded || failed
}

func pairingTokenValid(token string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return servicesvc.InvalArg("pairing token is invalid")
	}
	return nil
}

func pairParams(msg *computerv1.RegisterComputerRequest, now time.Time) (computerapp.PairCommand, error) {
	requestID := msg.GetRequestId()
	if requestID == "" {
		return computerapp.PairCommand{}, servicesvc.InvalArg("request id is required")
	}
	if err := pairingTokenValid(msg.GetPairingToken()); err != nil {
		return computerapp.PairCommand{}, err
	}
	if err := registrationKeyValid(msg.GetRegistrationKey()); err != nil {
		return computerapp.PairCommand{}, err
	}
	if err := validateName(msg.GetName()); err != nil {
		return computerapp.PairCommand{}, err
	}
	os, ok := osFromProto[msg.GetOs()]
	if !ok {
		return computerapp.PairCommand{}, servicesvc.InvalArg("operating system must be macos or linux")
	}
	arch, ok := archFromProto[msg.GetArch()]
	if !ok {
		return computerapp.PairCommand{}, servicesvc.InvalArg("architecture must be arm64 or amd64")
	}
	inventory, err := parseCapabilityInventory(msg.GetCapabilityInventory())
	if err != nil {
		return computerapp.PairCommand{}, err
	}
	return computerapp.PairCommand{
		RequestID: requestID, PairingToken: msg.GetPairingToken(),
		RegistrationKey: msg.GetRegistrationKey(), Name: msg.GetName(),
		OS: os, Arch: arch,
		CapabilityInventory: inventory, Now: now,
	}, nil
}

func registrationKeyValid(key string) error {
	if key == "" {
		return servicesvc.InvalArg("registration key is required")
	}
	if len(key) > 256 {
		return servicesvc.InvalArg("registration key is too long")
	}
	return nil
}

func validateName(name string) error {
	if !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return servicesvc.InvalArg("computer name is invalid")
	}
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 100 {
		return servicesvc.InvalArg("computer name must contain 1 to 100 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return servicesvc.InvalArg("computer name is invalid")
		}
	}
	return nil
}

func parseCapabilityInventory(v *computerv1.CapabilityInventoryDeclaration) (computerdomain.CapabilityInventory, error) {
	if v == nil || v.GetCredentialDelivery() == nil {
		return computerdomain.CapabilityInventory{}, invCap()
	}
	inv := computerdomain.CapabilityInventory{
		Engines:   make([]computerdomain.EngineCapability, 0, len(v.GetEngines())),
		Sandboxes: make([]computerdomain.SandboxCapability, 0, len(v.GetSandboxes())),
	}
	for _, e := range v.GetEngines() {
		engine, err := parseEngineCap(e)
		if err != nil {
			return computerdomain.CapabilityInventory{}, err
		}
		inv.Engines = append(inv.Engines, engine)
	}
	for _, s := range v.GetSandboxes() {
		sb, err := parseSandboxCap(s)
		if err != nil {
			return computerdomain.CapabilityInventory{}, err
		}
		inv.Sandboxes = append(inv.Sandboxes, sb)
	}
	cred, err := parseCredDeliveryCap(v.GetCredentialDelivery())
	if err != nil {
		return computerdomain.CapabilityInventory{}, err
	}
	inv.CredentialDelivery = cred
	if !inv.ValidDeclaration() {
		return computerdomain.CapabilityInventory{}, invCap()
	}
	return inv, nil
}

func parseEngineCap(v *computerv1.EngineCapability) (computerdomain.EngineCapability, error) {
	if v == nil {
		return computerdomain.EngineCapability{}, invCap()
	}
	e := computerdomain.EngineCapability{
		Version: v.GetVersion(), ProtocolVersion: v.GetProtocolVersion(),
		SupportsToolCalls: v.GetSupportsToolCalls(), SupportsCancel: v.GetSupportsCancel(),
	}
	switch v.GetEngine() {
	case agentv1.EngineKind_ENGINE_KIND_BUILTIN:
		e.Kind = computerdomain.EngineBuiltin
	case agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER:
		e.Kind = computerdomain.EngineCodexAdapter
	case agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER:
		e.Kind = computerdomain.EngineClaudeAdapter
	default:
		return computerdomain.EngineCapability{}, invCap()
	}
	switch v.GetHealth() {
	case computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY:
		e.Healthy = true
	case computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE:
	default:
		return computerdomain.EngineCapability{}, invCap()
	}
	seen := make(map[agentv1.ProviderProtocol]struct{}, len(v.GetProviderProtocols()))
	for _, pp := range v.GetProviderProtocols() {
		if _, dup := seen[pp]; dup {
			return computerdomain.EngineCapability{}, invCap()
		}
		seen[pp] = struct{}{}
		switch pp {
		case agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES:
			e.OpenAIResponses = true
		case agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES:
			e.AnthropicMessages = true
		default:
			return computerdomain.EngineCapability{}, invCap()
		}
	}
	if !e.Valid() {
		return computerdomain.EngineCapability{}, invCap()
	}
	return e, nil
}

func parseSandboxCap(v *computerv1.SandboxCapability) (computerdomain.SandboxCapability, error) {
	if v == nil {
		return computerdomain.SandboxCapability{}, invCap()
	}
	if v.GetProvider() == computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL &&
		v.GetIsolation() == computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL &&
		v.GetWorkspaceAccess() == computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE &&
		v.GetProcessControl() == computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP &&
		v.GetFilesystemIsolation() == computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE &&
		v.GetNetworkIsolation() == computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE &&
		v.GetSecretMaterialization() == computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT &&
		v.GetDaemonCrashCleanup() == computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE {
		return computerdomain.TrustedLocalSandboxCapability(), nil
	}
	return computerdomain.SandboxCapability{}, invCap()
}

func parseCredDeliveryCap(v *computerv1.CredentialDeliveryCapability) (computerdomain.CredentialDeliveryCapability, error) {
	if v == nil {
		return computerdomain.CredentialDeliveryCapability{}, invCap()
	}
	if v.GetHealth() == computerv1.CapabilityHealth_CAPABILITY_HEALTH_UNAVAILABLE {
		if v.GetAlgorithm() != computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_UNSPECIFIED ||
			v.GetStore() != computerv1.CredentialStore_CREDENTIAL_STORE_UNSPECIFIED ||
			v.GetKeyId() != "" || len(v.GetPublicKey()) != 0 {
			return computerdomain.CredentialDeliveryCapability{}, invCap()
		}
		return computerdomain.CredentialDeliveryCapability{}, nil
	}
	if v.GetHealth() != computerv1.CapabilityHealth_CAPABILITY_HEALTH_HEALTHY ||
		v.GetAlgorithm() != computerv1.CredentialDeliveryAlgorithm_CREDENTIAL_DELIVERY_ALGORITHM_X25519_XCHACHA20_POLY1305 ||
		len(v.GetPublicKey()) != 32 {
		return computerdomain.CredentialDeliveryCapability{}, invCap()
	}
	store := ""
	switch v.GetStore() {
	case computerv1.CredentialStore_CREDENTIAL_STORE_MACOS_KEYCHAIN:
		store = "macos_keychain"
	case computerv1.CredentialStore_CREDENTIAL_STORE_LINUX_SECRET_SERVICE:
		store = "linux_secret_service"
	default:
		return computerdomain.CredentialDeliveryCapability{}, invCap()
	}
	cap := computerdomain.CredentialDeliveryCapability{
		Healthy: true, Algorithm: "x25519_xchacha20_poly1305",
		Store: store, KeyID: v.GetKeyId(),
		PublicKey: base64.RawURLEncoding.EncodeToString(v.GetPublicKey()),
	}
	if !cap.Valid() {
		return computerdomain.CredentialDeliveryCapability{}, invCap()
	}
	return cap, nil
}

func invCap() error {
	return servicesvc.InvalArg("capability inventory is invalid")
}
