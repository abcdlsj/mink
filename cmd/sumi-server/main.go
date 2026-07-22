package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/sumi/internal/serverapp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		log.Fatal(err)
	}
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return serverapp.RunContext(ctx, args, stdout, stderr)
}
