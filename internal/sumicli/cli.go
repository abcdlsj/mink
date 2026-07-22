package sumicli

import (
	"context"
	"fmt"
	"io"

	"github.com/abcdlsj/sumi/internal/computerapp"
	"github.com/abcdlsj/sumi/internal/hostcli"
	"github.com/abcdlsj/sumi/internal/serverapp"
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return hostcli.Run(args, stdout)
	}
	switch args[0] {
	case "server":
		if len(args) < 2 || args[1] != "run" {
			return invalidCommand("server", "use 'sumi server run'")
		}
		return serverapp.RunServer(ctx, args[2:], stdout, stderr)
	case "computer":
		if len(args) < 2 || args[1] != "run" {
			return invalidCommand("computer", "use 'sumi computer run'")
		}
		return computerapp.RunContext(ctx, args[2:], stdin, stderr)
	case "auth":
		if len(args) < 2 || args[1] != "open" {
			return invalidCommand("auth", "use 'sumi auth open --human-key-file <file>'")
		}
		return serverapp.RunAuth(ctx, args[2:], stdout, stderr)
	default:
		return hostcli.Run(args, stdout)
	}
}

func invalidCommand(command, nextAction string) error {
	return &hostcli.Error{
		Message:    fmt.Sprintf("unsupported %s command", command),
		Code:       "INVALID_COMMAND",
		NextAction: nextAction,
	}
}
