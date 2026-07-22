package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/driver"
	"github.com/abcdlsj/sumi/internal/sandbox/trustedlocal"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
	if !bytes.Contains(stderr.Bytes(), []byte(" synchronized 0 assignments")) {
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
	if _, err := public.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: attempt.RegistrationKey, Name: attempt.Name, Os: osName, Arch: arch,
		RequestId: attempt.RequestID, PairingToken: attempt.PairingToken,
		SandboxCapability: mustTrustedLocalSandboxCapability(t),
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

func TestOnceImportsLegacyKeyFileAndThenUsesPersistedIdentity(t *testing.T) {
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
	registrationKey := "legacy-key-file-only-secret"
	registered, err := public.RegisterComputer(context.Background(), connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: registrationKey, Name: "Legacy host",
		Os: computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, Arch: computerv1.Architecture_ARCHITECTURE_ARM64,
		RequestId: uuid.NewString(), PairingToken: token,
	}))
	if err != nil {
		t.Fatal(err)
	}

	computerRoot := filepath.Join(t.TempDir(), "computer-root")
	if err := os.Mkdir(computerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(computerRoot, "computer.key")
	if err := os.WriteFile(keyPath, []byte(registrationKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--once", "--server", httpServer.URL, "--data-root", computerRoot,
		"--registration-key-file", keyPath, "--name", "Legacy host upgraded",
	}
	if err := RunContext(context.Background(), args, bytes.NewReader(nil), io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	identity, found, err := state.Identity(context.Background())
	if closeErr := state.Close(); err == nil {
		err = closeErr
	}
	if err != nil || !found || identity.ComputerID != registered.Msg.GetComputer().GetId() || identity.RegistrationKey != registrationKey {
		t.Fatalf("imported identity = %+v, %v, %v", identity, found, err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if err := RunContext(context.Background(), args, bytes.NewReader(nil), io.Discard); err != nil {
		t.Fatalf("restart without legacy key file: %v", err)
	}
	computers, err := owner.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 || computers.Msg.GetComputers()[0].GetId() != identity.ComputerID {
		t.Fatalf("computers after legacy recovery = %+v, %v", computers.Msg.GetComputers(), err)
	}
	assertTrustedLocalCapability(t, computers.Msg.GetComputers()[0])
}

func assertTrustedLocalCapability(t *testing.T, computer *computerv1.Computer) {
	t.Helper()
	if !proto.Equal(computer.GetSandboxCapability(), mustTrustedLocalSandboxCapability(t)) {
		t.Fatalf("computer sandbox capability = %+v", computer.GetSandboxCapability())
	}
}

func mustTrustedLocalSandboxCapability(t *testing.T) *computerv1.SandboxCapability {
	t.Helper()
	capability, err := computerhost.TrustedLocalSandboxCapability()
	if err != nil {
		t.Fatal(err)
	}
	return capability
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

func TestNewExternalExecutorValidatesConfigurationBeforeDaemonStarts(t *testing.T) {
	resolve := func(context.Context, string) (driver.Kind, error) {
		return driver.KindCodex, nil
	}
	provider, err := trustedlocal.New(trustedlocal.Config{ScratchRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if executor, err := newExternalExecutor(externalRuntimeConfig{}, provider, resolve); err != nil || executor != nil {
		t.Fatalf("unconfigured executor = %v, %v", executor, err)
	}
	valid := externalRuntimeConfig{
		enabled: true, driver: string(driver.KindCodex), executable: os.Args[0], hostPolicy: "trusted local",
		timeout: time.Second, terminationGrace: time.Second, outputLimit: 1024,
	}
	invalid := []externalRuntimeConfig{
		{enabled: true, driver: string(driver.KindCodex)},
		{enabled: true, driver: string(driver.KindNative), executable: os.Args[0], hostPolicy: "trusted local", timeout: time.Second, terminationGrace: time.Second, outputLimit: 1},
		{enabled: true, driver: string(driver.KindCodex), executable: "relative", hostPolicy: "trusted local", timeout: time.Second, terminationGrace: time.Second, outputLimit: 1},
		{enabled: true, driver: string(driver.KindCodex), executable: os.Args[0], hostPolicy: "trusted local", timeout: time.Second, terminationGrace: time.Second},
	}
	for _, config := range invalid {
		if executor, err := newExternalExecutor(config, provider, resolve); err == nil || executor != nil {
			t.Fatalf("invalid executor = %v, %v for %+v", executor, err, config)
		}
	}
	executor, err := newExternalExecutor(valid, provider, resolve)
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	eligible, err := executor.Eligible(context.Background(), "agent-1")
	if err != nil || !eligible {
		t.Fatalf("eligible = %t, %v", eligible, err)
	}
}

func TestDisabledExternalExecutorIsNotInjectedIntoDaemon(t *testing.T) {
	executor, err := externalExecutor("", t.TempDir(), externalRuntimeConfig{})
	if err != nil || executor != nil {
		t.Fatalf("disabled external executor = %v, %v", executor, err)
	}
	if config := newDaemonConfig("", t.TempDir(), nil, executor); config.Executor != nil {
		t.Fatalf("disabled external daemon executor = %#v", config.Executor)
	}
}

func TestParseExternalSecretRejectsRawValues(t *testing.T) {
	secret, err := parseExternalSecret("SUMI_TOKEN=computer.environment:DRIVER_TOKEN")
	if err != nil || secret.Name != "SUMI_TOKEN" || secret.Ref.Source != trustedlocal.SecretSourceComputerEnvironment || secret.Ref.Key != "DRIVER_TOKEN" {
		t.Fatalf("secret = %+v, %v", secret, err)
	}
	for _, value := range []string{"SUMI_TOKEN=value", "SUMI_TOKEN=other:DRIVER_TOKEN", "SUMI_TOKEN=computer.environment:", "=computer.environment:DRIVER_TOKEN"} {
		if _, err := parseExternalSecret(value); err == nil {
			t.Fatalf("invalid secret reference %q was accepted", value)
		}
	}
}
