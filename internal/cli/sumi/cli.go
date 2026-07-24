package sumi

import (
	"context"
	"fmt"
	"io"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	computercli "github.com/abcdlsj/sumi/internal/computer/cli"
	serverapp "github.com/abcdlsj/sumi/internal/server/app"
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return clicontract.Run(args, stdout)
	}
	switch args[0] {
	case "server":
		if len(args) < 2 {
			return invalidCommand("server", "use 'sumi server run|start|stop|restart|status'")
		}
		if args[1] == "run" {
			return serverapp.RunServer(ctx, args[2:], stdout, stderr)
		}
		return runService(ctx, "server", args[1:], stdout, stderr)
	case "computer":
		if len(args) >= 2 && isServiceAction(args[1]) {
			return runService(ctx, "computer", args[1:], stdout, stderr)
		}
		if len(args) < 2 || args[1] != "run" {
			return invalidCommand("computer", "use 'sumi computer run' or 'sumi computer start --pairing-code <code>'")
		}
		return computercli.RunContext(ctx, args[2:], stdin, stderr)
	case "install", "upgrade", "uninstall":
		return runInstallCommand(ctx, args[0], args[1:], stdin, stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	default:
		return clicontract.Run(args, stdout)
	}
}

func invalidCommand(command, nextAction string) error {
	return &clicontract.Error{
		Message:    fmt.Sprintf("unsupported %s command", command),
		Code:       "INVALID_COMMAND",
		NextAction: nextAction,
	}
}
