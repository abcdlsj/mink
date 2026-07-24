package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	computercli "github.com/abcdlsj/sumi/internal/computer/cli"
	"github.com/abcdlsj/sumi/internal/observability"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdin, os.Stderr); err != nil {
		observability.CategoryLogger(observability.New(observability.ComponentComputer, os.Stderr), observability.ComponentComputer, observability.CategoryLifecycle).Error("computer process failed", "event", "computer.process.failed", "err", err)
		os.Exit(1)
	}
}

func runContext(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) error {
	return computercli.RunContext(ctx, args, stdin, stderr)
}
