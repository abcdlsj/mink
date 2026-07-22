package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/abcdlsj/sumi/internal/computerapp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdin, os.Stderr); err != nil {
		log.Fatal(err)
	}
}

func runContext(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) error {
	return computerapp.RunContext(ctx, args, stdin, stderr)
}
