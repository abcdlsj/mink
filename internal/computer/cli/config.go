package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/configfile"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/home"
)

type commandConfig struct {
	serverURL        string
	serverEndpoint   endpoint.Endpoint
	httpClient       *http.Client
	dataRoot         string
	configPath       string
	pairingTokenFile string
	resetPairing     bool
	name             string
	once             bool
}

func parseConfig(args []string, stderr io.Writer, defaultRoot, hostname string) (commandConfig, error) {
	flags := flag.NewFlagSet("sumi-computer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverURL := flags.String("server", "http://127.0.0.1:8080", "Sumi Server URL")
	serverPin := flags.String("server-pin", "", "sha256/base64url Server SPKI pin")
	dataRoot := flags.String("data-root", defaultRoot, "Computer data root")
	pairingTokenFile := flags.String("pairing-token-file", "", "0600 pairing token file, or - for stdin")
	resetPairing := flags.Bool("reset-pairing-attempt", false, "Replace a definitively invalid unpaired attempt using a new --pairing-token-file")
	name := flags.String("name", hostname, "Computer display name")
	once := flags.Bool("once", false, "Register and synchronize pending assignments once")
	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}
	if flags.NArg() != 0 {
		return commandConfig{}, errors.New("unexpected positional arguments")
	}
	serverConfigured := false
	pinConfigured := false
	flags.Visit(func(visited *flag.Flag) {
		switch visited.Name {
		case "server":
			serverConfigured = true
		case "server-pin":
			pinConfigured = true
		}
	})
	layout, err := home.Ensure(*dataRoot)
	if err != nil {
		return commandConfig{}, err
	}
	stored, err := configfile.Load(layout.Config)
	if err != nil {
		return commandConfig{}, err
	}
	serverEndpoint, err := resolveCommandEndpoint(*serverURL, *serverPin, serverConfigured, pinConfigured, stored.Server)
	if err != nil {
		return commandConfig{}, err
	}
	if (*pairingTokenFile != "" || *resetPairing) && serverEndpoint.Identity.Kind != endpoint.IdentityLiteralLoopback {
		return commandConfig{}, errors.New("raw pairing token files are limited to literal-loopback Servers")
	}
	httpClient, err := endpoint.NewHTTPClient(serverEndpoint)
	if err != nil {
		return commandConfig{}, err
	}
	return commandConfig{
		serverURL: serverEndpoint.Origin, serverEndpoint: serverEndpoint, httpClient: httpClient,
		dataRoot: layout.Root, configPath: layout.Config,
		pairingTokenFile: *pairingTokenFile, resetPairing: *resetPairing, name: *name, once: *once,
	}, nil
}

func resolveCommandEndpoint(rawOrigin, rawPin string, originExplicit, pinExplicit bool, stored configfile.ServerConfig) (endpoint.Endpoint, error) {
	if stored.Origin == "" {
		if stored.Identity != "" || stored.SPKIPin != "" {
			return endpoint.Endpoint{}, errors.New("configured Server identity has no origin")
		}
		return endpoint.Parse(rawOrigin, rawPin)
	}
	configured, err := endpoint.FromIdentity(stored.Origin, endpoint.Identity{Kind: endpoint.IdentityKind(stored.Identity), SPKIPin: stored.SPKIPin})
	if err != nil {
		return endpoint.Endpoint{}, errors.New("configured Server endpoint is invalid")
	}
	if !originExplicit && !pinExplicit {
		return configured, nil
	}
	requestedPin := rawPin
	if !pinExplicit && rawOrigin == configured.Origin {
		requestedPin = configured.Identity.SPKIPin
	}
	requested, err := endpoint.Parse(rawOrigin, requestedPin)
	if err != nil {
		return endpoint.Endpoint{}, err
	}
	if requested.Origin != configured.Origin || requested.Identity != configured.Identity {
		return endpoint.Endpoint{}, errors.New("command Server endpoint conflicts with persisted config")
	}
	return configured, nil
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
