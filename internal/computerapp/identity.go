package computerapp

import (
	"context"
	"errors"
	"io"
	"log"
	"time"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/computerhost"
	"github.com/abcdlsj/sumi/internal/computerstate"
)

func resolveComputerIdentity(
	ctx context.Context,
	config commandConfig,
	stdin io.Reader,
	state *computerstate.State,
	osName computerv1.OperatingSystem,
	arch computerv1.Architecture,
) (computerstate.Identity, *computerhost.SyncResult, error) {
	identity, found, err := state.Identity(ctx)
	if err != nil {
		return computerstate.Identity{}, nil, err
	}
	if found {
		if config.pairingTokenFile != "" || config.resetPairing {
			return computerstate.Identity{}, nil, errors.New("paired computer does not accept another pairing token")
		}
		return identity, nil, nil
	}
	host := computerhost.New(hostConfig(config, state, "", osName, arch))
	attempt, attemptFound, err := state.PairingAttempt(ctx)
	if err != nil {
		return computerstate.Identity{}, nil, err
	}
	if config.resetPairing {
		if !attemptFound {
			return computerstate.Identity{}, nil, errors.New("pairing attempt not found")
		}
		if config.pairingTokenFile == "" {
			return computerstate.Identity{}, nil, errors.New("pairing token file is required to reset the pairing attempt")
		}
		token, err := computerhost.ReadPairingToken(config.pairingTokenFile, stdin)
		if err != nil {
			return computerstate.Identity{}, nil, err
		}
		if _, err := host.ReplacePairingAttempt(ctx, token, config.name, osName, arch, time.Now()); err != nil {
			return computerstate.Identity{}, nil, err
		}
		identity, err := requireComputerIdentity(ctx, state, "paired computer identity was not persisted after pairing replacement")
		return identity, nil, err
	}
	if attemptFound && config.pairingTokenFile != "" {
		token, err := computerhost.ReadPairingToken(config.pairingTokenFile, stdin)
		if err != nil {
			return computerstate.Identity{}, nil, err
		}
		if token != attempt.PairingToken {
			return computerstate.Identity{}, nil, errors.New("pairing token does not match persisted attempt")
		}
	}
	if !attemptFound && config.pairingTokenFile != "" {
		token, err := computerhost.ReadPairingToken(config.pairingTokenFile, stdin)
		if err != nil {
			return computerstate.Identity{}, nil, err
		}
		if err := computerhost.PreparePairing(ctx, state, config.serverURL, token, config.name, osName, arch, time.Now()); err != nil {
			return computerstate.Identity{}, nil, err
		}
		attemptFound = true
	}
	if attemptFound {
		if _, err := host.PairOnce(ctx); err != nil {
			return computerstate.Identity{}, nil, err
		}
		identity, err := requireComputerIdentity(ctx, state, "paired computer identity was not persisted")
		return identity, nil, err
	}
	key, err := computerhost.ReadRegistrationKey(config.registrationKeyFile)
	if err != nil {
		return computerstate.Identity{}, nil, err
	}
	result, err := computerhost.New(hostConfig(config, state, key, osName, arch)).SyncOnce(ctx)
	if err != nil {
		return computerstate.Identity{}, nil, err
	}
	identity, err = requireComputerIdentity(ctx, state, "legacy computer identity was not persisted")
	if err != nil {
		return computerstate.Identity{}, nil, err
	}
	if identity.ComputerID != result.ComputerID {
		return computerstate.Identity{}, nil, errors.New("legacy computer identity does not match synchronized computer")
	}
	return identity, &result, nil
}

func requireComputerIdentity(ctx context.Context, state *computerstate.State, missing string) (computerstate.Identity, error) {
	identity, found, err := state.Identity(ctx)
	if err != nil {
		return computerstate.Identity{}, err
	}
	if !found {
		return computerstate.Identity{}, errors.New(missing)
	}
	return identity, nil
}

func synchronizeOnce(
	ctx context.Context,
	config commandConfig,
	state *computerstate.State,
	identity computerstate.Identity,
	initial *computerhost.SyncResult,
	osName computerv1.OperatingSystem,
	arch computerv1.Architecture,
	stderr io.Writer,
) error {
	result := initial
	if result == nil {
		synchronized, err := computerhost.New(hostConfig(config, state, identity.RegistrationKey, osName, arch)).SyncOnce(ctx)
		if err != nil {
			return err
		}
		result = &synchronized
	}
	log.New(stderr, "", log.LstdFlags).Printf("Computer %s synchronized %d assignments", result.ComputerID, result.Assignments)
	return nil
}

func hostConfig(
	config commandConfig,
	state *computerstate.State,
	registrationKey string,
	osName computerv1.OperatingSystem,
	arch computerv1.Architecture,
) computerhost.Config {
	return computerhost.Config{
		ServerURL: config.serverURL, DataRoot: config.dataRoot, RegistrationKey: registrationKey,
		Name: config.name, OS: osName, Arch: arch, State: state,
	}
}
