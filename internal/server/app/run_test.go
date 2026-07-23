package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	servercore "github.com/abcdlsj/sumi/internal/server"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

const (
	testHumanCredential = "human-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	testHandoffToken    = "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"
)

func TestAuthCommandCreatesOneTimeBrowserURL(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != websession.CreateHandoffPath || request.Header.Get("Authorization") != "Bearer "+testHumanCredential {
			t.Fatalf("request = %s %s %v", request.Method, request.URL.Path, request.Header)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusCreated)
		fmt.Fprintf(response, `{"path":"%s/%s","expires_at":"%s"}`, websession.CreateHandoffPath, testHandoffToken, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
	}))
	defer server.Close()
	keyPath := secureCredentialFile(t, testHumanCredential)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := RunContext(context.Background(), []string{"auth", "--server", server.URL, "--human-key-file", keyPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(stdout.String()), server.URL+websession.CreateHandoffPath+"/"+testHandoffToken; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 || requests.Load() != 1 {
		t.Fatalf("stderr/requests = %q/%d", stderr.String(), requests.Load())
	}
	assertCLISecretQuiet(t, stdout.String()+stderr.String(), keyPath, testHumanCredential)
}

func TestAuthCommandFailsClosedOnCredentialFiles(t *testing.T) {
	tests := map[string]string{
		"missing": filepath.Join(t.TempDir(), "missing.key"),
		"0644":    insecureCredentialFile(t),
		"symlink": symlinkCredentialFile(t),
	}
	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := RunContext(context.Background(), []string{"auth", "--human-key-file", path}, &stdout, &stderr)
			if err == nil {
				t.Fatal("unsafe credential file was accepted")
			}
			assertCLISecretQuiet(t, err.Error()+stdout.String()+stderr.String(), path, testHumanCredential)
		})
	}
}

func TestAuthCommandRejectsRedirectAndUnsafeHandoff(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"redirect": func(response http.ResponseWriter, _ *http.Request) {
			http.Redirect(response, &http.Request{}, "http://example.com/stolen", http.StatusFound)
		},
		"absolute": func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusCreated)
			fmt.Fprintf(response, `{"path":"http://example.com/%s","expires_at":"%s"}`, testHandoffToken, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
		},
		"query": func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusCreated)
			fmt.Fprintf(response, `{"path":"%s/%s?leak=1","expires_at":"%s"}`, websession.CreateHandoffPath, testHandoffToken, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
		},
	}
	keyPath := secureCredentialFile(t, testHumanCredential)
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := RunContext(context.Background(), []string{"auth", "--server", server.URL, "--human-key-file", keyPath}, &stdout, &stderr)
			if err == nil || stdout.Len() != 0 {
				t.Fatalf("unsafe handoff result = %v, %q", err, stdout.String())
			}
			assertCLISecretQuiet(t, err.Error()+stderr.String(), keyPath, testHumanCredential)
		})
	}
}

func TestBrowserOriginDerivationRequiresLiteralLoopbackListen(t *testing.T) {
	tests := []struct {
		listen   string
		explicit string
		want     string
	}{
		{"127.0.0.1:8080", "", "http://127.0.0.1:8080"},
		{"[::1]:8080", "", "http://[::1]:8080"},
		{"localhost:8080", "", ""},
		{":8080", "", ""},
		{"0.0.0.0:8080", "", ""},
		{"0.0.0.0:8080", "http://127.0.0.1:8080", "http://127.0.0.1:8080"},
	}
	for _, test := range tests {
		got, err := resolveBrowserOrigin(test.listen, test.explicit)
		if err != nil || got != test.want {
			t.Fatalf("resolve %q/%q = %q, %v, want %q", test.listen, test.explicit, got, err, test.want)
		}
	}
	if _, err := resolveBrowserOrigin("127.0.0.1:8080", "http://192.0.2.4:8080"); err == nil {
		t.Fatal("remote explicit browser origin accepted")
	}
}

func TestRunServerStopsWhenContextIsCanceled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	var stderr lockedBuffer
	dataRoot := t.TempDir()
	if err := os.Chmod(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- RunServer(ctx, []string{
			"--listen", "127.0.0.1:0",
			"--data-root", dataRoot,
			"--web-root", "",
		}, &stdout, &stderr)
	}()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(stderr.String(), "Sumi Server listening on http://127.0.0.1:0") && time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("server exited before listening: %v", err)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !strings.Contains(stderr.String(), "Sumi Server listening on http://127.0.0.1:0") {
		t.Fatalf("server did not start: %q", stderr.String())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunServer() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunServer did not stop after context cancellation")
	}
	if stdout.Len() != 0 {
		t.Fatalf("RunServer() stdout = %q", stdout.String())
	}
}

func TestRunServerMigratesLegacyCredentialBeforeListening(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := filepath.Join(t.TempDir(), "sumi")
	legacy, err := servercore.New(context.Background(), servercore.Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dataRoot, "owner.key")
	legacyCredential, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var stderr lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- RunServer(ctx, []string{"--listen", "127.0.0.1:0", "--data-root", dataRoot, "--web-root", ""}, io.Discard, &stderr)
	}()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(stderr.String(), "Sumi Server listening") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(stderr.String(), "Sumi Server listening") {
		t.Fatalf("server did not listen after migration: %q", stderr.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy credential remains: %v", err)
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	currentCredential, err := os.ReadFile(userLayout.HumanCredential)
	if err != nil || !bytes.Equal(currentCredential, legacyCredential) {
		t.Fatalf("migrated credential mismatch: %v", err)
	}
}

func TestRunServerClosesInitializedServerWhenCredentialFinalizeFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := filepath.Join(t.TempDir(), "sumi")
	legacy, err := servercore.New(context.Background(), servercore.Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	previous := finalizeCredentialMigration
	finalizeCredentialMigration = func(*authority.CredentialMigration) error { return errors.New("injected finalize failure") }
	t.Cleanup(func() { finalizeCredentialMigration = previous })
	var stderr bytes.Buffer
	err = RunServer(context.Background(), []string{"--listen", "127.0.0.1:0", "--data-root", dataRoot, "--web-root", ""}, io.Discard, &stderr)
	if err == nil || strings.Contains(stderr.String(), "listening") {
		t.Fatalf("finalize failure = %v, stderr %q", err, stderr.String())
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := servercore.New(context.Background(), servercore.Config{DataRoot: dataRoot, BootstrapCredentialFile: userLayout.HumanCredential})
	if err != nil {
		t.Fatalf("initialized Server was not closed: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunServerCannotBypassMaintenanceLease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	if err := os.Chmod(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	maintenance, err := lifecycle.AcquireMaintenance(dataRoot, userLayout.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	err = RunServer(context.Background(), []string{"--data-root", dataRoot, "--web-root", "", "--listen", "127.0.0.1:0"}, io.Discard, io.Discard)
	if !errors.Is(err, lifecycle.ErrRuntimeActive) {
		t.Fatalf("Server run bypassed maintenance = %v", err)
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(payload)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func secureCredentialFile(t *testing.T, credential string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "human.key")
	if err := os.WriteFile(path, []byte(credential), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func insecureCredentialFile(t *testing.T) string {
	t.Helper()
	path := secureCredentialFile(t, testHumanCredential)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func symlinkCredentialFile(t *testing.T) string {
	t.Helper()
	target := secureCredentialFile(t, testHumanCredential)
	path := filepath.Join(t.TempDir(), "human.key")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertCLISecretQuiet(t *testing.T, output, path, credential string) {
	t.Helper()
	if strings.Contains(output, path) || strings.Contains(output, credential) {
		t.Fatalf("CLI output leaked credential material: %q", output)
	}
}
