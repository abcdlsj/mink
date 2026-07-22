package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/sumi/internal/hostcli"
	"github.com/abcdlsj/sumi/internal/sumicli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		hostcli.FormatError(os.Stderr, err)
		os.Exit(1)
	}
}

func runContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return sumicli.Run(ctx, args, stdin, stdout, stderr)
}
