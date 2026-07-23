package sumi

import (
	"context"
	"io"

	computercli "github.com/abcdlsj/sumi/internal/computer/cli"
)

func runComputer(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return invalidCommand("computer", "use 'sumi computer run' or 'sumi computer pair create|join|discard'")
	}
	switch args[0] {
	case "run":
		return computercli.RunContext(ctx, args[1:], stdin, stderr)
	case "pair":
		return computercli.RunPair(ctx, args[1:], stdout, stderr)
	default:
		return invalidCommand("computer", "use 'sumi computer run' or 'sumi computer pair create|join|discard'")
	}
}
