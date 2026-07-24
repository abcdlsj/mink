package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/sumi/internal/observability"
	serverapp "github.com/abcdlsj/sumi/internal/server/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		observability.CategoryLogger(observability.New(observability.ComponentServer, os.Stderr), observability.ComponentServer, observability.CategoryLifecycle).Error("server process failed", "event", "server.process.failed", "err", err)
		os.Exit(1)
	}
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return serverapp.RunContext(ctx, args, stdout, stderr)
}
