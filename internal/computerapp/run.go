package computerapp

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/abcdlsj/sumi/internal/computerhost"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/home"
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
	config, err := parseConfig(args, stderr, defaultRoot, hostname)
	if err != nil {
		return err
	}
	osName, arch, err := platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	state, err := computerstate.Open(config.dataRoot)
	if err != nil {
		return err
	}
	defer state.Close()
	identity, initialSync, err := resolveComputerIdentity(ctx, config, stdin, state, osName, arch)
	if err != nil {
		return err
	}
	if config.once {
		return synchronizeOnce(ctx, config, state, identity, initialSync, osName, arch, stderr)
	}
	executor, err := externalExecutor(config.serverURL, config.dataRoot, config.external)
	if err != nil {
		return err
	}
	if executor != nil {
		defer executor.Close()
	}
	return computerhost.NewDaemon(newDaemonConfig(config.serverURL, config.dataRoot, state, executor)).Run(ctx)
}
