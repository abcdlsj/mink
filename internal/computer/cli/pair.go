package cli

import (
	"context"
	"runtime"
	"time"

	"connectrpc.com/connect"
	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	"github.com/abcdlsj/sumi/internal/computer/enginefactory"
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/configfile"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/pairing"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

var pairJoinBeforeRPC = func() error { return nil }

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
	return joinPairingBundle(ctx, bundle, dataRoot, name)
}

func joinPairingBundle(ctx context.Context, bundle pairing.Bundle, dataRoot, name string) error {
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
			return pairCommandError("pairing code conflicts with the durable attempt", "PAIRING_CONFLICT", "retry the original connection command or wait for expiry")
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
	httpClient, err := endpoint.NewHTTPClient(serverEndpoint)
	if err != nil {
		return err
	}
	manager, err := credentialManager(ctx, state)
	if err != nil {
		return err
	}
	inventory, err := enginefactory.Discover().Inventory(manager)
	if err != nil {
		return err
	}
	host := computerhost.New(computerhost.Config{
		ServerURL: serverEndpoint.Origin, DataRoot: layout.Root, Name: name, OS: osName, Arch: architecture,
		HTTPClient: httpClient, State: state, CapabilityInventory: inventory,
	})
	if err := pairJoinBeforeRPC(); err != nil {
		return pairCommandError("pairing join state is unknown", "PAIRING_UNKNOWN", "run 'sumi computer run' to resume the durable attempt")
	}
	if _, err := host.PairOnce(ctx); err != nil {
		return mapPairingRPCError(err, "retry the same connection command to resume the durable attempt")
	}
	return nil
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
			return pairCommandError("pairing Server conflicts with persisted config", "PAIRING_CONFLICT", "use a connection code for the configured Server")
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
		return pairCommandError("pairing is invalid or expired", "PAIRING_EXPIRED", "copy a fresh connection command from Sumi")
	case connect.CodeAlreadyExists:
		return pairCommandError("pairing request conflicts with existing data", "PAIRING_CONFLICT", "retry the original connection command or copy a fresh one")
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return pairCommandError("pairing authorization was denied", "PAIRING_DENIED", "copy a fresh connection command from Sumi")
	default:
		return pairCommandError("pairing creation state is unknown", "PAIRING_UNKNOWN", unknownNext)
	}
}

func pairCommandError(message, code, next string) error {
	return &clicontract.Error{Message: message, Code: code, NextAction: next}
}
