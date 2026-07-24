package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/configfile"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/pairing"
	"github.com/abcdlsj/sumi/internal/userdirs"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	pairCreateBeforeRPC  = func(pairing.Bundle) error { return nil }
	pairJoinBeforeRemove = func() error { return nil }
	pairJoinBeforeRPC    = func() error { return nil }
	pairCreateRPC        = func(ctx context.Context, client computerv1connect.ComputerServiceClient, request *connect.Request[computerv1.CreateComputerPairingRequest]) (*connect.Response[computerv1.CreateComputerPairingResponse], error) {
		return client.CreateComputerPairing(ctx, request)
	}
)

func RunPair(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return pairCommandError("a pair command is required", "INVALID_COMMAND", "use 'sumi computer pair create|join|discard'")
	}
	switch args[0] {
	case "create":
		return runPairCreate(ctx, args[1:], stdout, stderr)
	case "join":
		return runPairJoin(ctx, args[1:], stdout, stderr)
	case "discard":
		return runPairDiscard(args[1:], stdout, stderr)
	default:
		return pairCommandError("unsupported pair command", "INVALID_COMMAND", "use 'sumi computer pair create|join|discard'")
	}
}

func runPairCreate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sumi computer pair create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverOrigin := flags.String("server", "", "Sumi Server origin")
	serverPin := flags.String("server-pin", "", "sha256/base64url Server SPKI pin")
	humanKeyFile := flags.String("human-key-file", "", "0600 Human credential file")
	out := flags.String("out", "", "new pairing bundle file")
	resume := flags.String("resume", "", "existing pairing bundle to replay")
	expiresIn := flags.Duration("expires-in", 10*time.Minute, "pairing lifetime up to 10m")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *humanKeyFile == "" {
		return pairCommandError("pair create arguments are invalid", "INVALID_ARGUMENT", "provide --server, --human-key-file, and --out; or --resume with --human-key-file")
	}
	visited := map[string]bool{}
	flags.Visit(func(value *flag.Flag) { visited[value.Name] = true })
	if *resume != "" {
		for _, incompatible := range []string{"server", "server-pin", "out", "expires-in"} {
			if visited[incompatible] {
				return pairCommandError("pair resume does not accept endpoint or expiry overrides", "INVALID_ARGUMENT", "retry with only --resume and --human-key-file")
			}
		}
		opened, err := pairing.Open(*resume)
		if err != nil {
			return pairCommandError("pairing bundle is missing or unsafe", "PAIRING_BUNDLE_INVALID", "restore the original bundle or wait for its Server expiry")
		}
		defer opened.Close()
		if err := opened.Bundle.ValidateAt(time.Now()); err != nil {
			return pairCommandError("pairing bundle is expired", "PAIRING_EXPIRED", "discard the expired bundle, then create a new pairing")
		}
		serverEndpoint, err := opened.Bundle.Endpoint()
		if err != nil {
			return pairCommandError("pairing bundle is invalid", "PAIRING_BUNDLE_INVALID", "restore the original bundle")
		}
		return submitPairCreate(ctx, serverEndpoint, opened.Bundle, *humanKeyFile, stdout)
	}
	if !visited["server"] || *serverOrigin == "" || *out == "" || *expiresIn <= 0 || *expiresIn > 10*time.Minute {
		return pairCommandError("pair create arguments are invalid", "INVALID_ARGUMENT", "provide --server, --human-key-file, --out, and an expiry no longer than 10m")
	}
	serverEndpoint, err := endpoint.Parse(*serverOrigin, *serverPin)
	if err != nil {
		return pairCommandError("Server endpoint is unsafe", "SERVER_ENDPOINT_INVALID", "use literal-loopback HTTP or trusted/pinned HTTPS")
	}
	bundle, err := pairing.New(serverEndpoint, time.Now().Add(*expiresIn))
	if err != nil {
		return pairCommandError("pairing secret generation failed", "PAIRING_CREATE_FAILED", "retry the command")
	}
	if _, err := authority.ReadCredentialFile(*humanKeyFile); err != nil {
		return pairCommandError("Human credential file is missing or unsafe", "HUMAN_CREDENTIAL_INVALID", "provide a no-follow 0600 Human credential file")
	}
	if err := pairing.WriteNew(*out, bundle); err != nil {
		return pairCommandError("pairing bundle could not be created without overwrite", "PAIRING_BUNDLE_EXISTS", "choose a new empty output path")
	}
	return submitPairCreate(ctx, serverEndpoint, bundle, *humanKeyFile, stdout)
}

func submitPairCreate(ctx context.Context, serverEndpoint endpoint.Endpoint, bundle pairing.Bundle, humanKeyFile string, stdout io.Writer) error {
	credential, err := authority.ReadCredentialFile(humanKeyFile)
	if err != nil {
		return pairCommandError("Human credential file is missing or unsafe", "HUMAN_CREDENTIAL_INVALID", "provide a no-follow 0600 Human credential file")
	}
	client, err := endpoint.NewHTTPClient(serverEndpoint)
	if err != nil {
		return pairCommandError("Server transport could not be created", "SERVER_ENDPOINT_INVALID", "verify the Server endpoint identity")
	}
	if err := pairCreateBeforeRPC(bundle); err != nil {
		return pairCommandError("pairing creation state is unknown", "PAIRING_UNKNOWN", "retry with --resume using the same bundle")
	}
	authorization := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+credential)
			return next(ctx, request)
		}
	}))
	computers := computerv1connect.NewComputerServiceClient(client, serverEndpoint.Origin, authorization)
	response, err := pairCreateRPC(ctx, computers, connect.NewRequest(&computerv1.CreateComputerPairingRequest{
		RequestId: bundle.RequestID, PairingToken: bundle.PairingToken, ExpiresAt: timestamppb.New(bundle.ExpiresAt),
	}))
	if err != nil {
		return mapPairingRPCError(err, "retry with --resume using the same bundle")
	}
	if response == nil || response.Msg == nil || uuid.Validate(response.Msg.GetPairingId()) != nil || response.Msg.GetExpiresAt() == nil ||
		response.Msg.GetExpiresAt().CheckValid() != nil || !response.Msg.GetExpiresAt().AsTime().Equal(bundle.ExpiresAt) {
		return pairCommandError("pairing response is invalid", "PAIRING_UNKNOWN", "retry with --resume using the same bundle")
	}
	_, err = fmt.Fprintln(stdout, "Pairing bundle ready.")
	return err
}

func runPairJoin(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return pairCommandError("Computer name is unavailable", "COMPUTER_NAME_INVALID", "provide --name")
	}
	flags := flag.NewFlagSet("sumi computer pair join", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "pairing bundle file")
	dataRoot := flags.String("data-root", defaultRoot, "Computer data root")
	name := flags.String("name", hostname, "Computer display name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *file == "" || *name == "" {
		return pairCommandError("pair join arguments are invalid", "INVALID_ARGUMENT", "provide --file and optionally --data-root and --name")
	}
	opened, err := pairing.Open(*file)
	if err != nil {
		return pairCommandError("pairing bundle is missing or unsafe", "PAIRING_BUNDLE_INVALID", "obtain the original no-follow 0600 bundle")
	}
	defer opened.Close()
	if err := opened.Bundle.ValidateAt(time.Now()); err != nil {
		return pairCommandError("pairing bundle is expired", "PAIRING_EXPIRED", "discard the expired bundle and obtain a new pairing")
	}
	if err := joinPairingBundle(ctx, opened.Bundle, *dataRoot, *name, opened.Remove); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Computer paired.")
	return err
}

// JoinPairingCode consumes a Web-issued connection code into durable Computer
// state. It intentionally never includes the code in output or returned errors.
func JoinPairingCode(ctx context.Context, code, dataRoot, name string) error {
	if code == "" || dataRoot == "" || name == "" {
		return pairCommandError("pairing code arguments are invalid", "INVALID_ARGUMENT", "provide --pairing-code and a Computer name")
	}
	bundle, err := pairing.DecodeCode(code)
	if err != nil {
		return pairCommandError("pairing code is invalid", "PAIRING_CODE_INVALID", "copy a fresh connection command from Sumi")
	}
	if err := bundle.ValidateAt(time.Now()); err != nil {
		return pairCommandError("pairing code is expired", "PAIRING_EXPIRED", "copy a fresh connection command from Sumi")
	}
	return joinPairingBundle(ctx, bundle, dataRoot, name, nil)
}

func joinPairingBundle(ctx context.Context, bundle pairing.Bundle, dataRoot, name string, consume func() error) error {
	serverEndpoint, err := bundle.Endpoint()
	if err != nil {
		return pairCommandError("pairing data is invalid", "PAIRING_INVALID", "obtain a new pairing from Sumi")
	}
	layout, err := home.Ensure(dataRoot)
	if err != nil {
		return err
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		return err
	}
	lease, err := lifecycle.AcquireRun(layout.Root, userLayout.Runtime, lifecycle.ComponentComputer)
	if err != nil {
		return pairCommandError("Computer runtime is active", "RUNTIME_ACTIVE", "stop the running Computer before joining")
	}
	defer lease.Close()
	state, err := computerstate.Open(layout.Root)
	if err != nil {
		return err
	}
	defer state.Close()
	if _, found, err := state.Identity(ctx); err != nil {
		return err
	} else if found {
		return pairCommandError("Computer is already paired", "COMPUTER_ALREADY_PAIRED", "run the existing Computer identity")
	}
	attempt, found, err := state.PairingAttempt(ctx)
	if err != nil {
		return err
	}
	osName, architecture, err := platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if found {
		if attempt.ServerURL != serverEndpoint.Origin || attempt.PairingToken != bundle.PairingToken {
			return pairCommandError("pairing bundle conflicts with the durable attempt", "PAIRING_CONFLICT", "resume the original attempt or wait for expiry")
		}
	}
	if err := savePairEndpoint(layout.Config, serverEndpoint); err != nil {
		return err
	}
	if !found {
		if err := computerhost.PreparePairing(ctx, state, serverEndpoint.Origin, bundle.PairingToken, name, osName, architecture, time.Now()); err != nil {
			return err
		}
	}
	if consume != nil {
		if err := pairJoinBeforeRemove(); err != nil {
			return pairCommandError("pairing join state is unknown", "PAIRING_UNKNOWN", "retry join with the same pairing data")
		}
		if err := consume(); err != nil {
			return pairCommandError("pairing bundle changed before consumption", "PAIRING_BUNDLE_CHANGED", "restore the original bundle and retry")
		}
	}
	httpClient, err := endpoint.NewHTTPClient(serverEndpoint)
	if err != nil {
		return err
	}
	host := computerhost.New(computerhost.Config{
		ServerURL: serverEndpoint.Origin, DataRoot: layout.Root, Name: name, OS: osName, Arch: architecture,
		HTTPClient: httpClient, State: state,
	})
	if err := pairJoinBeforeRPC(); err != nil {
		return pairCommandError("pairing join state is unknown", "PAIRING_UNKNOWN", "run 'sumi computer run' to resume the durable attempt")
	}
	if _, err := host.PairOnce(ctx); err != nil {
		return mapPairingRPCError(err, "retry the same connection command to resume the durable attempt")
	}
	return nil
}

func runPairDiscard(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sumi computer pair discard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "expired pairing bundle file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *file == "" {
		return pairCommandError("pair discard arguments are invalid", "INVALID_ARGUMENT", "provide --file")
	}
	opened, err := pairing.Open(*file)
	if err != nil {
		return pairCommandError("pairing bundle is missing or unsafe", "PAIRING_BUNDLE_INVALID", "provide the original no-follow 0600 bundle")
	}
	defer opened.Close()
	if err := opened.Discard(time.Now()); errors.Is(err, pairing.ErrStillValid) {
		return pairCommandError("pairing bundle is still valid", "PAIRING_STILL_VALID", "resume or join with the bundle, or wait for its expiry")
	} else if err != nil {
		return pairCommandError("pairing bundle changed before discard", "PAIRING_BUNDLE_CHANGED", "inspect the bundle path without following links")
	}
	_, err = fmt.Fprintln(stdout, "Expired pairing bundle discarded.")
	return err
}

func savePairEndpoint(path string, serverEndpoint endpoint.Endpoint) error {
	config, err := configfile.Load(path)
	if err != nil {
		return err
	}
	stored := config.Server
	if stored.Origin != "" {
		configured, err := endpoint.FromIdentity(stored.Origin, endpoint.Identity{Kind: endpoint.IdentityKind(stored.Identity), SPKIPin: stored.SPKIPin})
		if err != nil || configured.Origin != serverEndpoint.Origin || configured.Identity != serverEndpoint.Identity {
			return pairCommandError("pairing Server conflicts with persisted config", "PAIRING_CONFLICT", "use the bundle for the configured Server")
		}
		return nil
	}
	config.Server = configfile.ServerConfig{
		Origin: serverEndpoint.Origin, Identity: string(serverEndpoint.Identity.Kind), SPKIPin: serverEndpoint.Identity.SPKIPin,
	}
	return configfile.Save(path, config)
}

func mapPairingRPCError(err error, unknownNext string) error {
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument:
		return pairCommandError("pairing is invalid or expired", "PAIRING_EXPIRED", "discard after expiry, then create a new pairing")
	case connect.CodeAlreadyExists:
		return pairCommandError("pairing request conflicts with existing data", "PAIRING_CONFLICT", "keep the bundle as evidence and do not replace its token")
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return pairCommandError("pairing authorization was denied", "PAIRING_DENIED", "verify the Human credential and permission")
	default:
		return pairCommandError("pairing creation state is unknown", "PAIRING_UNKNOWN", unknownNext)
	}
}

func pairCommandError(message, code, next string) error {
	return &clicontract.Error{Message: message, Code: code, NextAction: next}
}
