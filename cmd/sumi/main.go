package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	sumi "github.com/abcdlsj/sumi/internal/cli/sumi"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		clicontract.FormatError(os.Stderr, err)
		os.Exit(1)
	}
}

func runContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return sumi.Run(ctx, args, stdin, stdout, stderr)
}
