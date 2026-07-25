package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSumiComputerPairsOnceAndPersistsGreenfieldIdentity(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("sumi-computer supports macOS and Linux")
	}
	binary := buildComputerBinary(t)
	serverRoot := t.TempDir()
	app, err := server.New(context.Background(), server.Config{DataRoot: serverRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	publicComputers := computerv1connect.NewComputerServiceClient(httpServer.Client(), httpServer.URL)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if _, err := publicComputers.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	computerRoot := t.TempDir()
	tokenFile := filepath.Join(t.TempDir(), "pairing.token")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary,
		"--server", httpServer.URL,
		"--data-root", computerRoot,
		"--pairing-token-file", tokenFile,
		"--name", "blackbox-computer",
		"--once",
	)
	var output bytes.Buffer
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("sumi-computer --once: %v\n%s", err, output.String())
	}
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	identity, found, err := state.Identity(context.Background())
	if err != nil || !found || identity.ServerURL != httpServer.URL || identity.ComputerID == "" || identity.RegistrationKey == "" {
		t.Fatalf("persisted identity = %+v, found=%t, err=%v", identity, found, err)
	}
	response, err := publicComputers.GetComputer(context.Background(), connect.NewRequest(&computerv1.GetComputerRequest{ComputerId: identity.ComputerID}))
	if err != nil || response.Msg.GetComputer().GetName() != "blackbox-computer" {
		t.Fatalf("server computer = %+v, %v", response, err)
	}
	if bytes.Contains(output.Bytes(), []byte(token)) || bytes.Contains(output.Bytes(), []byte(identity.RegistrationKey)) {
		t.Fatalf("computer output leaked credential material: %q", output.String())
	}
}

func buildComputerBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "sumi-computer")
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Dir = "."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sumi-computer: %v\n%s", err, output)
	}
	return binary
}
