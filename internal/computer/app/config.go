package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/sandbox"
)

type commandConfig struct {
	serverURL           string
	dataRoot            string
	registrationKeyFile string
	pairingTokenFile    string
	resetPairing        bool
	name                string
	once                bool
	external            externalRuntimeConfig
}

func parseConfig(args []string, stderr io.Writer, defaultRoot, hostname string) (commandConfig, error) {
	flags := flag.NewFlagSet("sumi-computer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverURL := flags.String("server", "http://127.0.0.1:8080", "Sumi Server URL")
	dataRoot := flags.String("data-root", defaultRoot, "Computer data root")
	registrationKeyFile := flags.String("registration-key-file", filepath.Join(defaultRoot, "computer.key"), "0600 registration key file")
	pairingTokenFile := flags.String("pairing-token-file", "", "0600 pairing token file, or - for stdin")
	resetPairing := flags.Bool("reset-pairing-attempt", false, "Replace a definitively invalid unpaired attempt using a new --pairing-token-file")
	name := flags.String("name", hostname, "Computer display name")
	once := flags.Bool("once", false, "Register and synchronize pending assignments once")
	driverKind := flags.String("external-driver", "", "External driver kind: codex or claude")
	executable := flags.String("external-executable", "", "Absolute external driver executable path")
	hostPolicy := flags.String("external-host-policy", "", "Required host policy for external driver runs")
	timeout := flags.Duration("external-timeout", 2*time.Minute, "External driver run timeout")
	terminationGrace := flags.Duration("external-termination-grace", 5*time.Second, "External driver TERM to KILL grace")
	outputLimit := flags.Int64("external-output-limit", 1<<20, "External driver stdout/stderr limit in bytes")
	var externalArgs []string
	var externalSecrets []sandbox.SecretEnvironmentVariable
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
		return commandConfig{}, err
	}
	if flags.NArg() != 0 {
		return commandConfig{}, errors.New("unexpected positional arguments")
	}
	externalConfigured := false
	flags.Visit(func(visited *flag.Flag) {
		switch visited.Name {
		case "external-driver", "external-executable", "external-host-policy", "external-timeout", "external-termination-grace", "external-output-limit", "external-arg", "external-secret":
			externalConfigured = true
		}
	})
	return commandConfig{
		serverURL: *serverURL, dataRoot: *dataRoot, registrationKeyFile: *registrationKeyFile,
		pairingTokenFile: *pairingTokenFile, resetPairing: *resetPairing, name: *name, once: *once,
		external: externalRuntimeConfig{
			enabled: externalConfigured, driver: *driverKind, executable: *executable,
			args: externalArgs, secrets: externalSecrets, hostPolicy: *hostPolicy,
			timeout: *timeout, terminationGrace: *terminationGrace, outputLimit: *outputLimit,
		},
	}, nil
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
