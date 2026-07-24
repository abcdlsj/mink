package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/observability"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

func RunContext(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) error {
	logger := observability.New(observability.ComponentComputer, stderr)
	lifecycleLogger := observability.CategoryLogger(logger, observability.ComponentComputer, observability.CategoryLifecycle)
	runtimeLogger := observability.CategoryLogger(logger, observability.ComponentComputer, observability.CategoryRuntime)
	driverLogger := observability.CategoryLogger(logger, observability.ComponentComputer, observability.CategoryDriver)
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
	identity, initialSync, err := resolveComputerIdentity(ctx, config, stdin, state, osName, arch)
	if err != nil {
		return err
	}
	runtimeLogger.Info("computer identity ready", "event", "computer.identity.ready", "computer_id", identity.ComputerID, "server_origin", identity.ServerURL)
	if config.once {
		return synchronizeOnce(ctx, config, state, identity, initialSync, osName, arch, logger)
	}
	config.external.logger = logger
	executor, err := externalExecutor(config.serverURL, config.dataRoot, config.httpClient, config.external)
	if err != nil {
		return err
	}
	if executor != nil {
		driverLogger.Info("external driver configured", "event", "driver.configured", "driver", config.external.driver)
		defer func() {
			if err := executor.Close(); err != nil {
				driverLogger.Error("external driver failed to close", "event", "driver.close.failed", "driver", config.external.driver, "err", err)
			}
		}()
	}
	daemonConfig := newDaemonConfig(config.serverURL, config.dataRoot, config.httpClient, state, executor)
	daemonConfig.Logger = logger
	return computerhost.NewDaemon(daemonConfig).Run(ctx)
}
