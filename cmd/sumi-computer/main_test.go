package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/google/uuid"
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
	tokenPath := filepath.Join(computerRoot, "pairing.token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--once", "--server", httpServer.URL, "--data-root", computerRoot,
		"--pairing-token-file", tokenPath, "--name", "Paired host",
	}
	if err := runContext(context.Background(), args, bytes.NewReader(nil)); err != nil {
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
	if err != nil || !found || identity.RegistrationKey == "" {
		t.Fatalf("paired identity = %+v, %v, %v", identity, found, err)
	}
	computers, err := owner.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 || computers.Msg.GetComputers()[0].GetId() != identity.ComputerID {
		t.Fatalf("computers = %+v, %v", computers.Msg.GetComputers(), err)
	}
	restartArgs := []string{"--once", "--server", httpServer.URL, "--data-root", computerRoot, "--name", "Paired host"}
	if err := runContext(context.Background(), restartArgs, bytes.NewReader(nil)); err != nil {
		t.Fatalf("paired restart: %v", err)
	}
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
	if err := run(args); err != nil {
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
	if err := run(args); err != nil {
		t.Fatalf("restart without legacy key file: %v", err)
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
