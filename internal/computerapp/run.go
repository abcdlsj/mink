package computerapp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/computerhost"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/driver"
	"github.com/abcdlsj/sumi/internal/driverexec"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/sandbox"
	"github.com/abcdlsj/sumi/internal/sandbox/trustedlocal"
)

func RunContext(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) error {
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("resolve computer name: %w", err)
	}
	flags := flag.NewFlagSet("sumi-computer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverURL := flags.String("server", "http://127.0.0.1:8080", "Sumi Server URL")
	dataRoot := flags.String("data-root", defaultRoot, "Computer data root")
	keyFile := flags.String("registration-key-file", filepath.Join(defaultRoot, "computer.key"), "0600 registration key file")
	pairingTokenFile := flags.String("pairing-token-file", "", "0600 pairing token file, or - for stdin")
	resetPairingAttempt := flags.Bool("reset-pairing-attempt", false, "Replace a definitively invalid unpaired attempt using a new --pairing-token-file")
	name := flags.String("name", hostname, "Computer display name")
	once := flags.Bool("once", false, "Register and synchronize pending assignments once")
	var externalArgs []string
	var externalSecrets []sandbox.SecretEnvironmentVariable
	externalDriver := flags.String("external-driver", "", "External driver kind: codex or claude")
	externalExecutable := flags.String("external-executable", "", "Absolute external driver executable path")
	externalHostPolicy := flags.String("external-host-policy", "", "Required host policy for external driver runs")
	externalTimeout := flags.Duration("external-timeout", 2*time.Minute, "External driver run timeout")
	externalTerminationGrace := flags.Duration("external-termination-grace", 5*time.Second, "External driver TERM to KILL grace")
	externalOutputLimit := flags.Int64("external-output-limit", 1<<20, "External driver stdout/stderr limit in bytes")
	flags.Func("external-arg", "External driver argument; repeat to add more", func(value string) error {
		externalArgs = append(externalArgs, value)
		return nil
	})
	flags.Func("external-secret", "External driver ENV_NAME=computer.environment:KEY reference; repeat to add more", func(value string) error {
		secret, err := parseExternalSecret(value)
		if err != nil {
			return err
		}
		externalSecrets = append(externalSecrets, secret)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	externalConfigured := false
	flags.Visit(func(flag *flag.Flag) {
		switch flag.Name {
		case "external-driver", "external-executable", "external-host-policy", "external-timeout", "external-termination-grace", "external-output-limit", "external-arg", "external-secret":
			externalConfigured = true
		}
	})
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
		log.New(stderr, "", log.LstdFlags).Printf("Computer %s synchronized %d assignments", result.ComputerID, result.Assignments)
		return nil
	}
	executor, err := externalExecutor(*serverURL, *dataRoot, externalRuntimeConfig{
		enabled: externalConfigured,
		driver:  *externalDriver, executable: *externalExecutable, args: externalArgs, secrets: externalSecrets,
		hostPolicy: *externalHostPolicy, timeout: *externalTimeout, terminationGrace: *externalTerminationGrace,
		outputLimit: *externalOutputLimit,
	})
	if err != nil {
		return err
	}
	if executor != nil {
		defer executor.Close()
	}
	return computerhost.NewDaemon(newDaemonConfig(*serverURL, *dataRoot, state, executor)).Run(ctx)
}

func newDaemonConfig(serverURL, dataRoot string, state *computerstate.State, executor *driverexec.ComputerExecutor) computerhost.DaemonConfig {
	config := computerhost.DaemonConfig{ServerURL: serverURL, DataRoot: dataRoot, State: state}
	if executor != nil {
		config.Executor = executor
	}
	return config
}

type externalRuntimeConfig struct {
	enabled          bool
	driver           string
	executable       string
	args             []string
	secrets          []sandbox.SecretEnvironmentVariable
	hostPolicy       string
	timeout          time.Duration
	terminationGrace time.Duration
	outputLimit      int64
}

func externalExecutor(serverURL, dataRoot string, config externalRuntimeConfig) (*driverexec.ComputerExecutor, error) {
	if !config.enabled {
		return nil, nil
	}
	kind := driver.Kind(config.driver)
	if (kind != driver.KindCodex && kind != driver.KindClaude) || config.executable == "" || config.hostPolicy == "" {
		return nil, errors.New("external driver, executable, and host policy must be configured together")
	}
	provider, err := trustedlocal.New(trustedlocal.Config{ScratchRoot: dataRoot, GracePeriod: config.terminationGrace})
	if err != nil {
		return nil, err
	}
	return newExternalExecutor(config, provider, func(ctx context.Context, agentID string) (driver.Kind, error) {
		client := agentv1connect.NewAgentServiceClient(http.DefaultClient, serverURL)
		response, err := client.GetAgent(ctx, connect.NewRequest(&agentv1.GetAgentRequest{AgentId: agentID}))
		if err != nil || response == nil || response.Msg == nil || response.Msg.GetAgent() == nil {
			return "", errors.New("resolve agent driver")
		}
		switch response.Msg.GetAgent().GetDriver() {
		case agentv1.Driver_DRIVER_CODEX:
			return driver.KindCodex, nil
		case agentv1.Driver_DRIVER_CLAUDE:
			return driver.KindClaude, nil
		case agentv1.Driver_DRIVER_NATIVE:
			return driver.KindNative, nil
		default:
			return "", errors.New("agent driver is invalid")
		}
	})
}

func newExternalExecutor(config externalRuntimeConfig, provider sandbox.Provider, resolve driverexec.AgentDriverResolver) (*driverexec.ComputerExecutor, error) {
	if !config.enabled {
		return nil, nil
	}
	kind := driver.Kind(config.driver)
	if (kind != driver.KindCodex && kind != driver.KindClaude) || config.executable == "" || config.hostPolicy == "" {
		return nil, errors.New("external driver, executable, and host policy must be configured together")
	}
	runner := driver.ProcessRunner{
		Path: config.executable, Args: config.args, Secrets: config.secrets, Provider: provider, Timeout: config.timeout,
		TerminationGrace: config.terminationGrace, MaxOutputBytes: config.outputLimit,
	}
	if err := runner.Validate(); err != nil {
		return nil, err
	}
	engine := driver.External{Kind: kind, Runner: runner}
	return driverexec.NewComputerExecutor(kind, engine, config.hostPolicy, resolve)
}

func parseExternalSecret(value string) (sandbox.SecretEnvironmentVariable, error) {
	name, reference, found := strings.Cut(value, "=")
	source, key, sourceFound := strings.Cut(reference, ":")
	if !found || !sourceFound || name == "" || key == "" || source != trustedlocal.SecretSourceComputerEnvironment ||
		strings.ContainsAny(name, "=\x00") || strings.ContainsAny(key, "=\x00") {
		return sandbox.SecretEnvironmentVariable{}, errors.New("external driver secret reference is invalid")
	}
	return sandbox.SecretEnvironmentVariable{Name: name, Ref: sandbox.SecretRef{Source: source, Key: key}}, nil
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
