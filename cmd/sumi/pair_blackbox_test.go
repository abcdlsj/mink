package main

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	servercore "github.com/abcdlsj/sumi/internal/server"
)

func TestUnifiedBinaryPairCreateJoinRestartAndQuietOutput(t *testing.T) {
	applicationRoot := t.TempDir()
	application, err := servercore.New(context.Background(), servercore.Config{DataRoot: applicationRoot})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	t.Cleanup(func() {
		server.Close()
		if err := application.Close(); err != nil {
			t.Errorf("close Server: %v", err)
		}
	})
	binary := buildUnifiedBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "pair.json")
	humanKey := filepath.Join(applicationRoot, "owner.key")
	create := exec.Command(binary, "computer", "pair", "create", "--server", server.URL, "--human-key-file", humanKey, "--out", bundle)
	create.Env = append(os.Environ(), "HOME="+home)
	createOutput, err := create.CombinedOutput()
	if err != nil {
		t.Fatalf("pair create: %v\n%s", err, createOutput)
	}
	computerRoot := filepath.Join(home, ".sumi")
	join := exec.Command(binary, "computer", "pair", "join", "--file", bundle, "--data-root", computerRoot, "--name", "Blackbox computer")
	join.Env = append(os.Environ(), "HOME="+home)
	joinOutput, err := join.CombinedOutput()
	if err != nil {
		t.Fatalf("pair join: %v\n%s", err, joinOutput)
	}
	if _, err := os.Lstat(bundle); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consumed bundle remains: %v", err)
	}
	restart := exec.Command(binary, "computer", "run", "--once", "--data-root", computerRoot)
	restart.Env = append(os.Environ(), "HOME="+home)
	restartOutput, err := restart.CombinedOutput()
	if err != nil {
		t.Fatalf("paired restart: %v\n%s", err, restartOutput)
	}
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	identity, found, identityErr := state.Identity(context.Background())
	_, attemptFound, attemptErr := state.PairingAttempt(context.Background())
	closeErr := state.Close()
	if identityErr != nil || attemptErr != nil || closeErr != nil || !found || attemptFound {
		t.Fatalf("blackbox state = %+v/%v attempt=%v errors=%v/%v/%v", identity, found, attemptFound, identityErr, attemptErr, closeErr)
	}
	quiet := string(createOutput) + string(joinOutput) + string(restartOutput)
	humanCredential, err := os.ReadFile(humanKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{humanKey, bundle, computerRoot, string(humanCredential), identity.RegistrationKey} {
		if strings.Contains(quiet, secret) {
			t.Fatalf("unified pair output leaked private material %q: %q", secret, quiet)
		}
	}
}

func buildUnifiedBinary(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve pair blackbox source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "sumi")
	command := exec.Command("go", "build", "-o", binary, "./cmd/sumi")
	command.Dir = root
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("build sumi: %v\n%s", err, output.String())
	}
	return binary
}
