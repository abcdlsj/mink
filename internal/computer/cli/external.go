package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/driver"
	driverexecutor "github.com/abcdlsj/sumi/internal/driver/executor"
	"github.com/abcdlsj/sumi/internal/sandbox"
	"github.com/abcdlsj/sumi/internal/sandbox/trustedlocal"
)

func newDaemonConfig(serverURL, dataRoot string, client *http.Client, state *computerstate.State, executor *driverexecutor.ComputerExecutor) computerhost.DaemonConfig {
	config := computerhost.DaemonConfig{ServerURL: serverURL, DataRoot: dataRoot, HTTPClient: client, State: state}
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

func externalExecutor(serverURL, dataRoot string, client *http.Client, config externalRuntimeConfig) (*driverexecutor.ComputerExecutor, error) {
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
		agents := agentv1connect.NewAgentServiceClient(client, serverURL)
		response, err := agents.GetAgent(ctx, connect.NewRequest(&agentv1.GetAgentRequest{AgentId: agentID}))
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

func newExternalExecutor(config externalRuntimeConfig, provider sandbox.Provider, resolve driverexecutor.AgentDriverResolver) (*driverexecutor.ComputerExecutor, error) {
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
	return driverexecutor.NewComputerExecutor(kind, engine, config.hostPolicy, resolve)
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
