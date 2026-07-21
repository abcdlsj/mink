package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/computerhost"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/home"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdin); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	return runContext(context.Background(), args, os.Stdin)
}

func runContext(ctx context.Context, args []string, stdin io.Reader) error {
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("resolve computer name: %w", err)
	}
	flags := flag.NewFlagSet("sumi-computer", flag.ContinueOnError)
	serverURL := flags.String("server", "http://127.0.0.1:8080", "Sumi Server URL")
	dataRoot := flags.String("data-root", defaultRoot, "Computer data root")
	keyFile := flags.String("registration-key-file", filepath.Join(defaultRoot, "computer.key"), "0600 registration key file")
	pairingTokenFile := flags.String("pairing-token-file", "", "0600 pairing token file, or - for stdin")
	resetPairingAttempt := flags.Bool("reset-pairing-attempt", false, "Replace a definitively invalid unpaired attempt using a new --pairing-token-file")
	name := flags.String("name", hostname, "Computer display name")
	once := flags.Bool("once", false, "Register and synchronize pending assignments once")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	osName, arch, err := platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	state, err := computerstate.Open(*dataRoot)
	if err != nil {
		return err
	}
	defer state.Close()
	identity, found, err := state.Identity(ctx)
	if err != nil {
		return err
	}
	key := identity.RegistrationKey
	var initialSync *computerhost.SyncResult
	host := computerhost.New(computerhost.Config{
		ServerURL: *serverURL, DataRoot: *dataRoot, RegistrationKey: key, Name: *name,
		OS: osName, Arch: arch, State: state,
	})
	if found && (*pairingTokenFile != "" || *resetPairingAttempt) {
		return errors.New("paired computer does not accept another pairing token")
	}
	if !found {
		attempt, attemptFound, err := state.PairingAttempt(ctx)
		if err != nil {
			return err
		}
		if *resetPairingAttempt {
			if !attemptFound {
				return errors.New("pairing attempt not found")
			}
			if *pairingTokenFile == "" {
				return errors.New("pairing token file is required to reset the pairing attempt")
			}
			token, err := computerhost.ReadPairingToken(*pairingTokenFile, stdin)
			if err != nil {
				return err
			}
			_, err = host.ReplacePairingAttempt(ctx, token, *name, osName, arch, time.Now())
			if err != nil {
				return err
			}
			identity, found, err = state.Identity(ctx)
			if err != nil || !found {
				return errors.New("paired computer identity was not persisted after pairing replacement")
			}
		} else {
			if attemptFound && *pairingTokenFile != "" {
				token, err := computerhost.ReadPairingToken(*pairingTokenFile, stdin)
				if err != nil {
					return err
				}
				if token != attempt.PairingToken {
					return errors.New("pairing token does not match persisted attempt")
				}
			}
			if !attemptFound && *pairingTokenFile != "" {
				token, err := computerhost.ReadPairingToken(*pairingTokenFile, stdin)
				if err != nil {
					return err
				}
				if err := computerhost.PreparePairing(ctx, state, *serverURL, token, *name, osName, arch, time.Now()); err != nil {
					return err
				}
				attemptFound = true
			}
			if attemptFound {
				if _, err := host.PairOnce(ctx); err != nil {
					return err
				}
				identity, found, err = state.Identity(ctx)
				if err != nil || !found {
					return errors.New("paired computer identity was not persisted")
				}
			} else {
				key, err = computerhost.ReadRegistrationKey(*keyFile)
				if err != nil {
					return err
				}
				legacyConfig := computerhost.Config{
					ServerURL: *serverURL, DataRoot: *dataRoot, RegistrationKey: key, Name: *name,
					OS: osName, Arch: arch, State: state,
				}
				result, err := computerhost.New(legacyConfig).SyncOnce(ctx)
				if err != nil {
					return err
				}
				identity, found, err = state.Identity(ctx)
				if err != nil || !found || identity.ComputerID != result.ComputerID {
					return errors.New("legacy computer identity was not persisted")
				}
				initialSync = &result
			}
		}
	}
	if *once {
		host = computerhost.New(computerhost.Config{
			ServerURL:       *serverURL,
			DataRoot:        *dataRoot,
			RegistrationKey: identity.RegistrationKey,
			Name:            *name,
			OS:              osName,
			Arch:            arch,
			State:           state,
		})
		result := initialSync
		if result == nil {
			synchronized, err := host.SyncOnce(ctx)
			if err != nil {
				return err
			}
			result = &synchronized
		}
		log.Printf("Computer %s synchronized %d assignments", result.ComputerID, result.Assignments)
		return nil
	}
	return computerhost.NewDaemon(computerhost.DaemonConfig{
		ServerURL: *serverURL, DataRoot: *dataRoot, State: state,
	}).Run(ctx)
}

func platform(goos, goarch string) (computerv1.OperatingSystem, computerv1.Architecture, error) {
	var osName computerv1.OperatingSystem
	switch goos {
	case "darwin":
		osName = computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS
	case "linux":
		osName = computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX
	default:
		return 0, 0, fmt.Errorf("unsupported operating system %q", goos)
	}
	var arch computerv1.Architecture
	switch goarch {
	case "arm64":
		arch = computerv1.Architecture_ARCHITECTURE_ARM64
	case "amd64":
		arch = computerv1.Architecture_ARCHITECTURE_AMD64
	default:
		return 0, 0, fmt.Errorf("unsupported architecture %q", goarch)
	}
	return osName, arch, nil
}
