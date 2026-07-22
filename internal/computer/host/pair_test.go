package host

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReadPairingTokenRequiresPrivateRegularFileOrStdin(t *testing.T) {
	token := testRuntimeToken(11)
	path := filepath.Join(t.TempDir(), "pairing.token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]struct {
		path  string
		stdin io.Reader
	}{
		"file":  {path: path},
		"stdin": {path: "-", stdin: strings.NewReader(token + "\n")},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ReadPairingToken(source.path, source.stdin)
			if err != nil || got != token {
				t.Fatalf("ReadPairingToken() = %q, %v", got, err)
			}
		})
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPairingToken(path, nil); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("public file error = %v", err)
	}
	symlink := filepath.Join(t.TempDir(), "pairing.token")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPairingToken(symlink, nil); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("symlink error = %v", err)
	}
	if _, err := ReadPairingToken("-", strings.NewReader("not-a-token")); err == nil || strings.Contains(err.Error(), "not-a-token") {
		t.Fatalf("invalid stdin error = %v", err)
	}
}

func TestPairOnceReplaysPersistedAttemptAfterCommittedResponseIsLost(t *testing.T) {
	api := openHostTestServer(t, t.TempDir())
	defer api.close(t)
	token := testRuntimeToken(12)
	if _, err := api.ownerComputers.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: token, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	computerRoot := filepath.Join(t.TempDir(), "computer")
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	random := bytes.NewReader(bytes.Repeat([]byte{19}, 32))
	if err := preparePairing(context.Background(), state, api.http.URL, token, "paired host",
		computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64, time.Now(), random); err != nil {
		t.Fatal(err)
	}
	attempt, found, err := state.PairingAttempt(context.Background())
	if err != nil || !found {
		t.Fatalf("pairing attempt = %+v, %v, %v", attempt, found, err)
	}
	faultClient := *api.http.Client()
	faultClient.Transport = &loseCommittedResponseTransport{base: faultClient.Transport}
	first := New(Config{ServerURL: api.http.URL, DataRoot: computerRoot, State: state, HTTPClient: &faultClient})
	if _, err := first.PairOnce(context.Background()); err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), attempt.RegistrationKey) {
		t.Fatalf("lost response error = %v", err)
	}
	replacementToken := testRuntimeToken(13)
	recovered, err := New(Config{ServerURL: api.http.URL, DataRoot: computerRoot, State: state, HTTPClient: api.http.Client()}).ReplacePairingAttempt(
		context.Background(), replacementToken, "replacement must not run",
		computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, computerv1.Architecture_ARCHITECTURE_AMD64, time.Now(),
	)
	if err != nil || !recovered {
		t.Fatalf("replace request after committed response loss = recovered %v, %v", recovered, err)
	}
	identity, found, err := state.Identity(context.Background())
	if err != nil || !found || identity.ComputerID == "" || identity.RegistrationKey != attempt.RegistrationKey {
		t.Fatalf("identity = %+v, %v, %v", identity, found, err)
	}
	if _, found, err := state.PairingAttempt(context.Background()); err != nil || found {
		t.Fatalf("pairing attempt after completion = %v, %v", found, err)
	}
	computers, err := api.ownerComputers.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 {
		t.Fatalf("computers = %v, %v", len(computers.Msg.GetComputers()), err)
	}
}

func TestReplacePairingAttemptKeepsOldAttemptOnUnavailable(t *testing.T) {
	computerRoot := filepath.Join(t.TempDir(), "computer")
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	oldToken := testRuntimeToken(14)
	if err := preparePairing(context.Background(), state, "http://127.0.0.1:1", oldToken, "offline host",
		computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64,
		time.Now(), bytes.NewReader(bytes.Repeat([]byte{20}, 32))); err != nil {
		t.Fatal(err)
	}
	expected, found, err := state.PairingAttempt(context.Background())
	if err != nil || !found {
		t.Fatalf("pairing attempt = %+v, %v, %v", expected, found, err)
	}
	recovered, err := New(Config{ServerURL: expected.ServerURL, DataRoot: computerRoot, State: state}).ReplacePairingAttempt(
		context.Background(), testRuntimeToken(15), "new host",
		computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX, computerv1.Architecture_ARCHITECTURE_AMD64, time.Now(),
	)
	if err == nil || recovered {
		t.Fatalf("offline replacement = recovered %v, %v", recovered, err)
	}
	actual, found, err := state.PairingAttempt(context.Background())
	if err != nil || !found || actual != expected {
		t.Fatalf("pairing attempt after unavailable = %+v, %v, %v", actual, found, err)
	}
}

func TestReplacePairingAttemptReplaysSameNewTokenAfterCommitResponseLoss(t *testing.T) {
	api := openHostTestServer(t, t.TempDir())
	defer api.close(t)
	oldToken := testRuntimeToken(16)
	newToken := testRuntimeToken(17)
	if _, err := api.ownerComputers.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: oldToken, ExpiresAt: timestamppb.New(time.Now().Add(100 * time.Millisecond)),
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ownerComputers.CreateComputerPairing(context.Background(), connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: uuid.NewString(), PairingToken: newToken, ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	})); err != nil {
		t.Fatal(err)
	}
	computerRoot := filepath.Join(t.TempDir(), "computer")
	state, err := computerstate.Open(computerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := preparePairing(context.Background(), state, api.http.URL, oldToken, "replace retry host",
		computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64,
		time.Now(), bytes.NewReader(bytes.Repeat([]byte{21}, 32))); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	faultClient := *api.http.Client()
	faultClient.Transport = &loseCommittedResponseTransport{base: faultClient.Transport, target: 2}
	host := New(Config{ServerURL: api.http.URL, DataRoot: computerRoot, State: state, HTTPClient: &faultClient})
	if recovered, err := host.ReplacePairingAttempt(context.Background(), newToken, "replace retry host",
		computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64, time.Now()); err == nil || recovered {
		t.Fatalf("first replacement = recovered %v, %v", recovered, err)
	}
	attempt, found, err := state.PairingAttempt(context.Background())
	if err != nil || !found || attempt.PairingToken != newToken {
		t.Fatalf("replacement attempt after response loss = %+v, %v, %v", attempt, found, err)
	}
	retry := New(Config{ServerURL: api.http.URL, DataRoot: computerRoot, State: state, HTTPClient: api.http.Client()})
	recovered, err := retry.ReplacePairingAttempt(context.Background(), newToken, "replace retry host",
		computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS, computerv1.Architecture_ARCHITECTURE_ARM64, time.Now())
	if err != nil || !recovered {
		t.Fatalf("replacement replay = recovered %v, %v", recovered, err)
	}
	if _, found, err := state.PairingAttempt(context.Background()); err != nil || found {
		t.Fatalf("pairing attempt after replacement replay = %v, %v", found, err)
	}
	computers, err := api.ownerComputers.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{}))
	if err != nil || len(computers.Msg.GetComputers()) != 1 {
		t.Fatalf("computers after replacement replay = %v, %v", len(computers.Msg.GetComputers()), err)
	}
}

type loseCommittedResponseTransport struct {
	base   http.RoundTripper
	target int
	count  int
}

func (t *loseCommittedResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	t.count++
	target := t.target
	if target == 0 {
		target = 1
	}
	if t.count != target {
		return response, nil
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return nil, errors.New("response lost after commit")
}
