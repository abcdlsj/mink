package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/configfile"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/pairing"
	servercore "github.com/abcdlsj/sumi/internal/server"
)

func TestPairCreateJoinPersistsEndpointAndConsumesRawToken(t *testing.T) {
	server, humanKey := newPairServer(t)
	bundlePath := filepath.Join(t.TempDir(), "pair.json")
	var createOutput bytes.Buffer
	if err := RunPair(context.Background(), []string{
		"create", "--server", server.URL, "--human-key-file", humanKey, "--out", bundlePath,
	}, &createOutput, io.Discard); err != nil {
		t.Fatal(err)
	}
	opened, err := pairing.Open(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	bundle := opened.Bundle
	opened.Close()
	computerRoot := filepath.Join(t.TempDir(), "computer")
	var joinOutput bytes.Buffer
	if err := RunPair(context.Background(), []string{
		"join", "--file", bundlePath, "--data-root", computerRoot, "--name", "Paired computer",
	}, &joinOutput, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(bundlePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consumed bundle remains: %v", err)
	}
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	identity, identityFound, identityErr := state.Identity(context.Background())
	_, attemptFound, attemptErr := state.PairingAttempt(context.Background())
	if closeErr := state.Close(); identityErr == nil {
		identityErr = closeErr
	}
	if identityErr != nil || attemptErr != nil || !identityFound || attemptFound || identity.RegistrationKey == "" {
		t.Fatalf("paired state = identity %+v/%v, attempt %v, errors %v/%v", identity, identityFound, attemptFound, identityErr, attemptErr)
	}
	config, err := configfile.Load(filepath.Join(computerRoot, "config.toml"))
	if err != nil || config.Server.Origin != server.URL || config.Server.Identity != string(endpoint.IdentityLiteralLoopback) {
		t.Fatalf("persisted endpoint = %+v, %v", config.Server, err)
	}
	quiet := createOutput.String() + joinOutput.String()
	for _, secret := range []string{bundle.PairingToken, humanKey, bundlePath, computerRoot, identity.RegistrationKey} {
		if strings.Contains(quiet, secret) {
			t.Fatalf("pair output leaked private material %q: %q", secret, quiet)
		}
	}
}

func TestPairCreateResumeReplaysPreRPCAndLostResponse(t *testing.T) {
	server, humanKey := newPairServer(t)
	for _, test := range []struct {
		name   string
		inject func(t *testing.T, original func(context.Context, computerv1connect.ComputerServiceClient, *connect.Request[computerv1.CreateComputerPairingRequest]) (*connect.Response[computerv1.CreateComputerPairingResponse], error))
	}{
		{"pre rpc", func(t *testing.T, _ func(context.Context, computerv1connect.ComputerServiceClient, *connect.Request[computerv1.CreateComputerPairingRequest]) (*connect.Response[computerv1.CreateComputerPairingResponse], error)) {
			previous := pairCreateBeforeRPC
			failed := false
			pairCreateBeforeRPC = func(pairing.Bundle) error {
				if !failed {
					failed = true
					return errors.New("injected crash")
				}
				return nil
			}
			t.Cleanup(func() { pairCreateBeforeRPC = previous })
		}},
		{"lost response", func(t *testing.T, original func(context.Context, computerv1connect.ComputerServiceClient, *connect.Request[computerv1.CreateComputerPairingRequest]) (*connect.Response[computerv1.CreateComputerPairingResponse], error)) {
			first := true
			pairCreateRPC = func(ctx context.Context, client computerv1connect.ComputerServiceClient, request *connect.Request[computerv1.CreateComputerPairingRequest]) (*connect.Response[computerv1.CreateComputerPairingResponse], error) {
				response, err := original(ctx, client, request)
				if err == nil && first {
					first = false
					return nil, connect.NewError(connect.CodeUnavailable, errors.New("injected lost response"))
				}
				return response, err
			}
			t.Cleanup(func() { pairCreateRPC = original })
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			originalRPC := pairCreateRPC
			test.inject(t, originalRPC)
			bundlePath := filepath.Join(t.TempDir(), "pair.json")
			err := RunPair(context.Background(), []string{
				"create", "--server", server.URL, "--human-key-file", humanKey, "--out", bundlePath,
			}, io.Discard, io.Discard)
			assertPairCode(t, err, "PAIRING_UNKNOWN")
			opened, openErr := pairing.Open(bundlePath)
			if openErr != nil {
				t.Fatal(openErr)
			}
			before := opened.Bundle
			opened.Close()
			if err := RunPair(context.Background(), []string{
				"create", "--resume", bundlePath, "--human-key-file", humanKey,
			}, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			opened, openErr = pairing.Open(bundlePath)
			if openErr != nil {
				t.Fatal(openErr)
			}
			if opened.Bundle != before {
				t.Fatalf("resume changed bundle = %+v, want %+v", opened.Bundle, before)
			}
			opened.Close()
		})
	}
}

func TestPairCreateNoClobberAndDiscardExpiry(t *testing.T) {
	server, humanKey := newPairServer(t)
	path := filepath.Join(t.TempDir(), "pair.json")
	if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RunPair(context.Background(), []string{
		"create", "--server", server.URL, "--human-key-file", humanKey, "--out", path,
	}, io.Discard, io.Discard)
	assertPairCode(t, err, "PAIRING_BUNDLE_EXISTS")
	payload, err := os.ReadFile(path)
	if err != nil || string(payload) != "sentinel" {
		t.Fatalf("pre-existing file = %q, %v", payload, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	serverEndpoint, err := endpoint.Parse(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := pairing.New(serverEndpoint, time.Now().UTC().Add(time.Minute))
	if err != nil || pairing.WriteNew(path, bundle) != nil {
		t.Fatalf("write valid bundle = %v", err)
	}
	err = RunPair(context.Background(), []string{"discard", "--file", path}, io.Discard, io.Discard)
	assertPairCode(t, err, "PAIRING_STILL_VALID")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("valid bundle was discarded: %v", err)
	}
	opened, err := pairing.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	opened.Bundle.ExpiresAt = time.Now().UTC().Add(-time.Second)
	expired := opened.Bundle
	opened.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := pairing.WriteNew(path, expired); err != nil {
		t.Fatal(err)
	}
	if err := RunPair(context.Background(), []string{"discard", "--file", path}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired bundle remains: %v", err)
	}
}

func TestPairJoinCrashRecoveryBeforeAndAfterBundleRemoval(t *testing.T) {
	for _, test := range []struct {
		name        string
		installHook func(t *testing.T)
		resumeJoin  bool
	}{
		{"before removal", func(t *testing.T) {
			previous := pairJoinBeforeRemove
			failed := false
			pairJoinBeforeRemove = func() error {
				if !failed {
					failed = true
					return errors.New("injected crash")
				}
				return nil
			}
			t.Cleanup(func() { pairJoinBeforeRemove = previous })
		}, true},
		{"after removal", func(t *testing.T) {
			previous := pairJoinBeforeRPC
			failed := false
			pairJoinBeforeRPC = func() error {
				if !failed {
					failed = true
					return errors.New("injected crash")
				}
				return nil
			}
			t.Cleanup(func() { pairJoinBeforeRPC = previous })
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, humanKey := newPairServer(t)
			bundlePath := filepath.Join(t.TempDir(), "pair.json")
			if err := RunPair(context.Background(), []string{"create", "--server", server.URL, "--human-key-file", humanKey, "--out", bundlePath}, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			computerRoot := filepath.Join(t.TempDir(), "computer")
			test.installHook(t)
			err := RunPair(context.Background(), []string{"join", "--file", bundlePath, "--data-root", computerRoot}, io.Discard, io.Discard)
			assertPairCode(t, err, "PAIRING_UNKNOWN")
			state, openErr := computerstate.Open(computerRoot)
			if openErr != nil {
				t.Fatal(openErr)
			}
			_, attemptFound, attemptErr := state.PairingAttempt(context.Background())
			state.Close()
			if attemptErr != nil || !attemptFound {
				t.Fatalf("durable attempt after crash = %v, %v", attemptFound, attemptErr)
			}
			if test.resumeJoin {
				if _, statErr := os.Stat(bundlePath); statErr != nil {
					t.Fatalf("bundle missing before removal: %v", statErr)
				}
				if err := RunPair(context.Background(), []string{"join", "--file", bundlePath, "--data-root", computerRoot}, io.Discard, io.Discard); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, statErr := os.Lstat(bundlePath); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("bundle remains after removal: %v", statErr)
				}
				if err := RunContext(context.Background(), []string{"--once", "--data-root", computerRoot}, bytes.NewReader(nil), io.Discard); err != nil {
					t.Fatal(err)
				}
			}
			state, openErr = computerstate.Open(computerRoot)
			if openErr != nil {
				t.Fatal(openErr)
			}
			_, identityFound, identityErr := state.Identity(context.Background())
			_, attemptFound, attemptErr = state.PairingAttempt(context.Background())
			state.Close()
			if identityErr != nil || attemptErr != nil || !identityFound || attemptFound {
				t.Fatalf("recovered pairing = identity %v, attempt %v, errors %v/%v", identityFound, attemptFound, identityErr, attemptErr)
			}
		})
	}
}

func TestPairErrorClassification(t *testing.T) {
	for code, want := range map[connect.Code]string{
		connect.CodeInvalidArgument:  "PAIRING_EXPIRED",
		connect.CodeAlreadyExists:    "PAIRING_CONFLICT",
		connect.CodePermissionDenied: "PAIRING_DENIED",
		connect.CodeUnavailable:      "PAIRING_UNKNOWN",
	} {
		assertPairCode(t, mapPairingRPCError(connect.NewError(code, errors.New("quiet")), "retry"), want)
	}
}

func newPairServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	application, err := servercore.New(context.Background(), servercore.Config{DataRoot: root})
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
	return server, filepath.Join(root, "owner.key")
}

func assertPairCode(t *testing.T, err error, want string) {
	t.Helper()
	var structured *clicontract.Error
	if !errors.As(err, &structured) || structured.Code != want {
		t.Fatalf("pair error = %#v, want code %s", err, want)
	}
}
