package domain

import (
	"encoding/base64"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type OperatingSystem string

const (
	OperatingSystemMacOS OperatingSystem = "macos"
	OperatingSystemLinux OperatingSystem = "linux"
)

type Architecture string

const (
	ArchitectureARM64 Architecture = "arm64"
	ArchitectureAMD64 Architecture = "amd64"
)

type SandboxCapability struct {
	Provider              string
	Isolation             string
	WorkspaceAccess       string
	ProcessControl        string
	FilesystemIsolation   string
	NetworkIsolation      string
	SecretMaterialization string
	DaemonCrashCleanup    string
}

func TrustedLocalSandboxCapability() SandboxCapability {
	return SandboxCapability{
		Provider: "trusted_local", Isolation: "trusted_local", WorkspaceAccess: "direct_read_write",
		ProcessControl: "context_process_group", FilesystemIsolation: "none", NetworkIsolation: "none",
		SecretMaterialization: "ephemeral_environment", DaemonCrashCleanup: "none",
	}
}

func (capability SandboxCapability) Valid() bool {
	return capability == TrustedLocalSandboxCapability()
}

type EngineKind string

const (
	EngineBuiltin       EngineKind = "builtin"
	EngineCodexAdapter  EngineKind = "codex_adapter"
	EngineClaudeAdapter EngineKind = "claude_adapter"
)

type ProviderProtocol string

const (
	ProviderOpenAIResponses   ProviderProtocol = "openai_responses"
	ProviderAnthropicMessages ProviderProtocol = "anthropic_messages"
)

type EngineCapability struct {
	Kind              EngineKind
	Version           string
	ProtocolVersion   uint32
	SupportsToolCalls bool
	SupportsCancel    bool
	OpenAIResponses   bool
	AnthropicMessages bool
	Healthy           bool
}

func (capability EngineCapability) Valid() bool {
	if capability.Kind != EngineBuiltin && capability.Kind != EngineCodexAdapter && capability.Kind != EngineClaudeAdapter {
		return false
	}
	if !validDescriptorString(capability.Version) || capability.ProtocolVersion == 0 {
		return false
	}
	if capability.Kind == EngineBuiltin {
		return capability.OpenAIResponses || capability.AnthropicMessages
	}
	return !capability.OpenAIResponses && !capability.AnthropicMessages
}

type CredentialDeliveryCapability struct {
	Healthy   bool
	Algorithm string
	Store     string
	KeyID     string
	PublicKey string
}

func (capability CredentialDeliveryCapability) Valid() bool {
	if !capability.Healthy {
		return capability.Algorithm == "" && capability.Store == "" && capability.KeyID == "" && capability.PublicKey == ""
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(capability.PublicKey)
	return err == nil && len(publicKey) == 32 && base64.RawURLEncoding.EncodeToString(publicKey) == capability.PublicKey &&
		capability.Algorithm == "x25519_xchacha20_poly1305" &&
		(capability.Store == "macos_keychain" || capability.Store == "linux_secret_service") &&
		validDescriptorString(capability.KeyID)
}

func validDescriptorString(value string) bool {
	if value == "" || len(value) > 255 || !utf8.ValidString(value) || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

type CapabilityInventory struct {
	Revision           uint64
	Engines            []EngineCapability
	Sandboxes          []SandboxCapability
	CredentialDelivery CredentialDeliveryCapability
	DeclaredAt         time.Time
}

func (inventory CapabilityInventory) ValidDeclaration() bool {
	if inventory.Revision != 0 || !inventory.DeclaredAt.IsZero() || len(inventory.Sandboxes) != 1 || !inventory.Sandboxes[0].Valid() || !inventory.CredentialDelivery.Valid() {
		return false
	}
	seen := make(map[EngineKind]struct{}, len(inventory.Engines))
	for _, engine := range inventory.Engines {
		if !engine.Valid() {
			return false
		}
		if _, duplicate := seen[engine.Kind]; duplicate {
			return false
		}
		seen[engine.Kind] = struct{}{}
	}
	return true
}

func TrustedLocalCapabilityInventory(engines ...EngineCapability) CapabilityInventory {
	return CapabilityInventory{
		Engines: engines, Sandboxes: []SandboxCapability{TrustedLocalSandboxCapability()},
	}
}
