package host

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func ReadPairingToken(path string, stdin io.Reader) (string, error) {
	var payload []byte
	var err error
	if path == "-" {
		if stdin == nil {
			return "", errors.New("pairing token stdin is unavailable")
		}
		payload, err = io.ReadAll(io.LimitReader(stdin, 257))
		if err != nil {
			return "", fmt.Errorf("read pairing token from stdin: %w", err)
		}
	} else {
		payload, err = readSecureFile(path, "pairing token")
		if err != nil {
			return "", err
		}
	}
	token := strings.TrimSpace(string(payload))
	if !canonicalSecret(token) {
		return "", errors.New("pairing token is invalid")
	}
	return token, nil
}

func PreparePairing(ctx context.Context, state *computerstate.State, serverURL, token, name string, operatingSystem computerv1.OperatingSystem, architecture computerv1.Architecture, now time.Time) error {
	return preparePairing(ctx, state, serverURL, token, name, operatingSystem, architecture, now, rand.Reader)
}

func preparePairing(ctx context.Context, state *computerstate.State, serverURL, token, name string, operatingSystem computerv1.OperatingSystem, architecture computerv1.Architecture, now time.Time, random io.Reader) error {
	registrationKey, err := randomSecret(random)
	if err != nil {
		return err
	}
	osName, archName, err := platformNames(operatingSystem, architecture)
	if err != nil {
		return err
	}
	return state.SavePairingAttempt(ctx, computerstate.PairingAttempt{
		ServerURL: serverURL, PairingToken: token, RequestID: uuid.NewString(), RegistrationKey: registrationKey,
		Name: name, OS: osName, Arch: archName, CreatedAt: now,
	})
}

func (h *Host) PairOnce(ctx context.Context) (string, error) {
	if h.config.State == nil {
		return "", errors.New("computer state is required for pairing")
	}
	attempt, found, err := h.config.State.PairingAttempt(ctx)
	if err != nil {
		return "", err
	}
	if !found {
		identity, identityFound, err := h.config.State.Identity(ctx)
		if err != nil {
			return "", err
		}
		if identityFound {
			return identity.ComputerID, nil
		}
		return "", errors.New("pairing attempt not found")
	}
	if attempt.ServerURL != h.config.ServerURL {
		return "", errors.New("pairing attempt belongs to a different Server")
	}
	operatingSystem, architecture, err := platformValues(attempt.OS, attempt.Arch)
	if err != nil {
		return "", err
	}
	inventory, err := h.capabilityInventory()
	if err != nil {
		return "", err
	}
	response, err := h.computers.RegisterComputer(ctx, connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: attempt.RegistrationKey, Name: attempt.Name, Os: operatingSystem, Arch: architecture,
		RequestId: attempt.RequestID, PairingToken: attempt.PairingToken, CapabilityInventory: inventory,
	}))
	if err != nil {
		return "", connect.NewError(connect.CodeOf(err), errors.New("pair computer request failed"))
	}
	computer := response.Msg.GetComputer()
	if computer == nil || computer.GetCreatedAt() == nil || computer.GetCreatedAt().CheckValid() != nil {
		return "", errors.New("pair computer returned an invalid identity")
	}
	if err := h.config.State.CompletePairing(ctx, computerstate.Identity{
		ServerURL: attempt.ServerURL, ComputerID: computer.GetId(), RegistrationKey: attempt.RegistrationKey,
		PairedAt: computer.GetCreatedAt().AsTime(),
	}); err != nil {
		return "", fmt.Errorf("persist paired computer identity: %w", err)
	}
	return computer.GetId(), nil
}

func (h *Host) ReplacePairingAttempt(ctx context.Context, pairingToken, name string, operatingSystem computerv1.OperatingSystem, architecture computerv1.Architecture, now time.Time) (bool, error) {
	if h.config.State == nil {
		return false, errors.New("computer state is required for pairing replacement")
	}
	attempt, found, err := h.config.State.PairingAttempt(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		return false, errors.New("pairing attempt not found")
	}
	if _, err := h.PairOnce(ctx); err == nil {
		return true, nil
	} else if connect.CodeOf(err) != connect.CodeInvalidArgument {
		return false, errors.New("pairing attempt replacement requires a definitive invalid or expired Server response")
	}
	if !canonicalSecret(pairingToken) || pairingToken == attempt.PairingToken {
		return false, errors.New("replacement pairing token is invalid")
	}
	osName, archName, err := platformNames(operatingSystem, architecture)
	if err != nil {
		return false, err
	}
	registrationKey, err := randomSecret(rand.Reader)
	if err != nil {
		return false, err
	}
	replacement := computerstate.PairingAttempt{
		ServerURL: attempt.ServerURL, PairingToken: pairingToken, RequestID: uuid.NewString(), RegistrationKey: registrationKey,
		Name: name, OS: osName, Arch: archName, CreatedAt: now,
	}
	if err := h.config.State.ReplacePairingAttempt(ctx, attempt, replacement); err != nil {
		return false, err
	}
	_, err = h.PairOnce(ctx)
	return false, err
}

func readSecureFile(path, kind string) ([]byte, error) {
	fileDescriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s file: %w", kind, err)
	}
	file := os.NewFile(uintptr(fileDescriptor), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect %s file: %w", kind, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s file is not a regular file", kind)
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%s file mode is %o, want 600", kind, info.Mode().Perm())
	}
	payload, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return nil, fmt.Errorf("read %s file: %w", kind, err)
	}
	if len(payload) > 4096 {
		return nil, fmt.Errorf("%s file is too large", kind)
	}
	return payload, nil
}

func randomSecret(random io.Reader) (string, error) {
	payload := make([]byte, 32)
	if _, err := io.ReadFull(random, payload); err != nil {
		return "", fmt.Errorf("generate computer credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func canonicalSecret(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func platformNames(operatingSystem computerv1.OperatingSystem, architecture computerv1.Architecture) (string, string, error) {
	osName := map[computerv1.OperatingSystem]string{
		computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS: "macos",
		computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX: "linux",
	}[operatingSystem]
	archName := map[computerv1.Architecture]string{
		computerv1.Architecture_ARCHITECTURE_ARM64: "arm64",
		computerv1.Architecture_ARCHITECTURE_AMD64: "amd64",
	}[architecture]
	if osName == "" || archName == "" {
		return "", "", errors.New("computer platform is invalid")
	}
	return osName, archName, nil
}

func platformValues(osName, archName string) (computerv1.OperatingSystem, computerv1.Architecture, error) {
	operatingSystem := map[string]computerv1.OperatingSystem{
		"macos": computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS,
		"linux": computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX,
	}[osName]
	architecture := map[string]computerv1.Architecture{
		"arm64": computerv1.Architecture_ARCHITECTURE_ARM64,
		"amd64": computerv1.Architecture_ARCHITECTURE_AMD64,
	}[archName]
	if operatingSystem == computerv1.OperatingSystem_OPERATING_SYSTEM_UNSPECIFIED || architecture == computerv1.Architecture_ARCHITECTURE_UNSPECIFIED {
		return 0, 0, errors.New("persisted computer platform is invalid")
	}
	return operatingSystem, architecture, nil
}
