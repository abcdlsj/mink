package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/abcdlsj/sumi/internal/computer/enginefactory"
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/credential"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/observability"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

func RunContext(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) error {
	logger := observability.New(observability.ComponentComputer, stderr)
	lifecycleLogger := observability.CategoryLogger(logger, observability.ComponentComputer, observability.CategoryLifecycle)
	runtimeLogger := observability.CategoryLogger(logger, observability.ComponentComputer, observability.CategoryRuntime)
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("resolve computer name: %w", err)
	}
	config, err := parseConfig(args, stderr, defaultRoot, hostname)
	if err != nil {
		return err
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		return err
	}
	lease, err := lifecycle.AcquireRun(config.dataRoot, userLayout.Runtime, lifecycle.ComponentComputer)
	if err != nil {
		return err
	}
	defer lease.Close()
	lifecycleLogger.Info("computer runtime lease acquired", "event", "computer.lease.acquired")
	osName, arch, err := platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	state, err := computerstate.Open(config.dataRoot)
	if err != nil {
		return err
	}
	defer func() {
		if err := state.Close(); err != nil {
			lifecycleLogger.Error("computer state failed to close", "event", "computer.state.close.failed", "err", err)
		}
	}()
	lifecycleLogger.Info("computer state opened", "event", "computer.state.opened")
	discovery := enginefactory.Discover()
	manager, err := credentialManager(ctx, state)
	if err != nil {
		return err
	}
	inventory, err := discovery.Inventory(manager)
	if err != nil {
		return err
	}
	identity, initialSync, err := resolveComputerIdentity(ctx, config, stdin, state, osName, arch, inventory)
	if err != nil {
		return err
	}
	runtimeLogger.Info("computer identity ready", "event", "computer.identity.ready", "computer_id", identity.ComputerID, "server_origin", identity.ServerURL)
	if config.once {
		return synchronizeOnce(ctx, config, state, identity, initialSync, osName, arch, inventory, logger)
	}
	factory, err := enginefactory.New(enginefactory.Config{
		Discovery: discovery, CredentialManager: manager, State: state, HTTPClient: config.httpClient, Logger: logger,
		ServerURL: config.serverURL,
	})
	if err != nil {
		return err
	}
	supervisor, err := computerruntime.NewSupervisor(factory)
	if err != nil {
		return err
	}
	defer supervisor.Close()
	daemonConfig := computerhost.DaemonConfig{
		ServerURL: config.serverURL, DataRoot: config.dataRoot, HTTPClient: config.httpClient, State: state,
		RuntimeSupervisor: supervisor, CredentialManager: manager, CapabilityInventory: inventory,
	}
	daemonConfig.Logger = logger
	return computerhost.NewDaemon(daemonConfig).Run(ctx)
}

func credentialManager(ctx context.Context, state *computerstate.State) (*credential.Manager, error) {
	facility, available := credential.CurrentFacility()
	if !available {
		return nil, nil
	}
	return credential.NewManager(ctx, state, facility, nil, nil)
}
