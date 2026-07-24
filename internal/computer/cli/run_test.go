package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/computer/enginefactory"
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/configfile"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/abcdlsj/sumi/internal/userdirs"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "sumi-computer-cli-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		panic(err)
	}
	previous := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.Setenv("HOME", previous)
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestOncePairsFromPrivateTokenFileAndPersistsIdentity(t *testing.T) {
	serverRoot := t.TempDir()
	app, err := server.New(context.Background(), server.Config{DataRoot: serverRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	defer app.Close()
	credential, err := authority.ReadCredentialFile(filepath.Join(serverRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	authorization := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+credential)
			return next(ctx, request)
		}
	}))
	owner := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL, authorization)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if _, err := owner.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	computerRoot := filepath.Join(t.TempDir(), "computer-root")
	if err := os.Mkdir(computerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	replacementToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{45}, 32))
	if _, err := owner.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: replacementToken, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(computerRoot, "replacement-pairing.token")
	if err := os.WriteFile(tokenPath, []byte(replacementToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--once", "--server", httpServer.URL, "--data-root", computerRoot,
		"--pairing-token-file", tokenPath, "--name", "Paired host",
	}
	var stderr bytes.Buffer
	if err := RunContext(context.Background(), args, bytes.NewReader(nil), &stderr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("event=placement.sync.completed")) || !bytes.Contains(stderr.Bytes(), []byte("assignments=0")) {
		t.Fatalf("RunContext() stderr = %q", stderr.String())
	}
	if bytes.Contains(stderr.Bytes(), []byte(replacementToken)) || bytes.Contains(stderr.Bytes(), []byte(tokenPath)) {
		t.Fatalf("RunContext() stderr leaked pairing material: %q", stderr.String())
	}
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	identity, found, err := state.Identity(context.Background())
	if closeErr := state.Close(); err == nil {
		err = closeErr
	}
	if err != nil || !found || identity.RegistrationKey == "" {
		t.Fatalf("paired identity = %+v, %v, %v", identity, found, err)
	}
	computers, err := owner.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 || computers.Msg.GetComputers()[0].GetId() != identity.ComputerID {
		t.Fatalf("computers = %+v, %v", computers.Msg.GetComputers(), err)
	}
	assertTrustedLocalCapability(t, computers.Msg.GetComputers()[0])
	restartArgs := []string{"--once", "--server", httpServer.URL, "--data-root", computerRoot, "--name", "Paired host"}
	if err := RunContext(context.Background(), restartArgs, bytes.NewReader(nil), io.Discard); err != nil {
		t.Fatalf("paired restart: %v", err)
	}
}

func TestResetExpiredUnconsumedPairingAttemptUsesNewTokenWithoutOldHandoffFile(t *testing.T) {
	serverRoot := t.TempDir()
	app, err := server.New(context.Background(), server.Config{DataRoot: serverRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	defer app.Close()
	credential, err := authority.ReadCredentialFile(filepath.Join(serverRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	authorization := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+credential)
			return next(ctx, request)
		}
	}))
	owner := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL, authorization)
	oldToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{41}, 32))
	if _, err := owner.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: oldToken, ExpiresAt: timestamppb.New(time.Now().Add(100 * time.Millisecond)),
	})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	computerRoot := filepath.Join(t.TempDir(), "computer-root")
	if err := os.Mkdir(computerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTokenPath := filepath.Join(computerRoot, "old-pairing.token")
	if err := os.WriteFile(oldTokenPath, []byte(oldToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	baseArgs := []string{"--server", httpServer.URL, "--data-root", computerRoot, "--name", "Reset host"}
	initialArgs := append(append([]string{}, baseArgs...), "--once", "--pairing-token-file", oldTokenPath)
	if err := RunContext(context.Background(), initialArgs, bytes.NewReader(nil), io.Discard); err == nil || bytes.Contains([]byte(err.Error()), []byte(oldToken)) {
		t.Fatalf("expired pairing error = %v", err)
	}
	if err := os.Remove(oldTokenPath); err != nil {
		t.Fatal(err)
	}
	newToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{43}, 32))
	if _, err := owner.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: newToken, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	newTokenPath := filepath.Join(computerRoot, "new-pairing.token")
	if err := os.WriteFile(newTokenPath, []byte(newToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetArgs := append(append([]string{}, baseArgs...), "--once", "--reset-pairing-attempt", "--pairing-token-file", newTokenPath)
	if err := RunContext(context.Background(), resetArgs, bytes.NewReader(nil), io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	identity, identityFound, err := state.Identity(context.Background())
	_, attemptFound, attemptErr := state.PairingAttempt(context.Background())
	if closeErr := state.Close(); err == nil {
		err = closeErr
	}
	if err != nil || attemptErr != nil || !identityFound || identity.ComputerID == "" || attemptFound {
		t.Fatalf("state after replacement = identity %+v/%v, attempt %v, errors %v/%v", identity, identityFound, attemptFound, err, attemptErr)
	}
	if _, err := os.Stat(newTokenPath); err != nil {
		t.Fatalf("new operator handoff file was unexpectedly removed: %v", err)
	}
	computers, err := owner.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 {
		t.Fatalf("computers after reset and re-pair = %+v, %v", computers.Msg.GetComputers(), err)
	}
	assertTrustedLocalCapability(t, computers.Msg.GetComputers()[0])
}

func TestResetPairingAttemptReplaysConsumedAttemptInsteadOfClearingIt(t *testing.T) {
	serverRoot := t.TempDir()
	app, err := server.New(context.Background(), server.Config{DataRoot: serverRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	defer app.Close()
	credential, err := authority.ReadCredentialFile(filepath.Join(serverRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	authorization := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+credential)
			return next(ctx, request)
		}
	}))
	owner := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL, authorization)
	public := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL)
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{44}, 32))
	if _, err := owner.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	computerRoot := filepath.Join(t.TempDir(), "computer-root")
	if err := os.Mkdir(computerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	osName, arch, err := platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if err := computerhost.PreparePairing(context.Background(), state, httpServer.URL, token, "Consumed host", osName, arch, time.Now()); err != nil {
		t.Fatal(err)
	}
	attempt, found, err := state.PairingAttempt(context.Background())
	if err != nil || !found {
		t.Fatalf("pairing attempt = %+v, %v, %v", attempt, found, err)
	}
	manager, err := credentialManager(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := enginefactory.Discover().Inventory(manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := public.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: attempt.RegistrationKey, Name: attempt.Name, Os: osName, Arch: arch,
		RequestId: attempt.RequestID, PairingToken: attempt.PairingToken,
		CapabilityInventory: inventory,
	})); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	replacementToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{45}, 32))
	if _, err := owner.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: replacementToken, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(computerRoot, "replacement-pairing.token")
	if err := os.WriteFile(tokenPath, []byte(replacementToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetArgs := []string{
		"--once", "--server", httpServer.URL, "--data-root", computerRoot, "--name", "Consumed host",
		"--reset-pairing-attempt", "--pairing-token-file", tokenPath,
	}
	if err := RunContext(context.Background(), resetArgs, bytes.NewReader(nil), io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err = computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	identity, identityFound, err := state.Identity(context.Background())
	_, attemptFound, attemptErr := state.PairingAttempt(context.Background())
	if closeErr := state.Close(); err == nil {
		err = closeErr
	}
	if err != nil || attemptErr != nil || !identityFound || identity.ComputerID == "" || attemptFound {
		t.Fatalf("recovered state = identity %+v/%v, attempt %v, errors %v/%v", identity, identityFound, attemptFound, err, attemptErr)
	}
	computers, err := owner.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 || computers.Msg.GetComputers()[0].GetId() != identity.ComputerID {
		t.Fatalf("computers after consumed reset request = %+v, %v", computers.Msg.GetComputers(), err)
	}
	assertTrustedLocalCapability(t, computers.Msg.GetComputers()[0])
}

func assertTrustedLocalCapability(t *testing.T, computer *computerv1.Computer) {
	t.Helper()
	inventory := computer.GetCapabilityInventory()
	if inventory == nil || inventory.GetRevision() == 0 || len(inventory.GetSandboxes()) != 1 ||
		inventory.GetSandboxes()[0].GetFilesystemIsolation() != computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE {
		t.Fatalf("computer capability inventory = %+v", inventory)
	}
}

func TestPlatform(t *testing.T) {
	tests := []struct {
		goos   string
		goarch string
		os     computerv1.OperatingSystem
		arch   computerv1.Architecture
	}{
		{"darwin", "arm64", computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64},
		{"linux", "amd64", computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, computerv1.Architecture_ARCHITECTURE_AMD64},
	}
	for _, test := range tests {
		osName, arch, err := platform(test.goos, test.goarch)
		if err != nil {
			t.Fatal(err)
		}
		if osName != test.os || arch != test.arch {
			t.Fatalf("platform(%q, %q) = %v, %v", test.goos, test.goarch, osName, arch)
		}
	}
	if _, _, err := platform("windows", "amd64"); err == nil {
		t.Fatal("windows error = nil")
	}
	if _, _, err := platform("linux", "386"); err == nil {
		t.Fatal("386 error = nil")
	}
}

func TestComputerRunUsesPersistedValidatedEndpointAndLifecycleGate(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	layout, err := home.Ensure(root)
	if err != nil {
		t.Fatal(err)
	}
	config := configfile.Default()
	config.Server = configfile.ServerConfig{Origin: "https://example.com", Identity: string(endpoint.IdentitySystemTrust)}
	if err := configfile.Save(layout.Config, config); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseConfig([]string{"--data-root", root, "--once"}, io.Discard, root, "computer")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.serverURL != "https://example.com" || parsed.serverEndpoint.Identity.Kind != endpoint.IdentitySystemTrust || parsed.httpClient == nil {
		t.Fatalf("parsed endpoint = %+v", parsed)
	}
	if _, err := parseConfig([]string{"--data-root", root, "--server", "https://other.example.com", "--once"}, io.Discard, root, "computer"); err == nil {
		t.Fatal("endpoint override conflict was accepted")
	}
	if _, err := parseConfig([]string{"--data-root", root, "--pairing-token-file", "token"}, io.Discard, root, "computer"); err == nil {
		t.Fatal("raw remote pairing token was accepted")
	}

	config.Server = configfile.ServerConfig{}
	if err := configfile.Save(layout.Config, config); err != nil {
		t.Fatal(err)
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	maintenance, err := lifecycle.AcquireMaintenance(root, userLayout.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	err = RunContext(context.Background(), []string{"--data-root", root, "--once"}, bytes.NewReader(nil), io.Discard)
	if !errors.Is(err, lifecycle.ErrRuntimeActive) {
		t.Fatalf("Computer run bypassed maintenance = %v", err)
	}
}
