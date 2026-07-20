package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}

	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	dataRoot := flag.String("data-root", defaultRoot, "Sumi data root")
	webRoot := flag.String("web-root", "web/dist", "Production Web root")
	ownerKeyFile := flag.String("owner-key-file", "", "0600 bootstrap owner credential file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := server.New(ctx, server.Config{DataRoot: *dataRoot, WebRoot: *webRoot, BootstrapCredentialFile: *ownerKeyFile})
	if err != nil {
		return fmt.Errorf("initialize server: %w", err)
	}
	defer app.Close()

	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		log.Printf("Sumi Server listening on http://%s", *listen)
		serveErrors <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
