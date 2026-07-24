package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/observability"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

func RunContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "auth" {
		return RunAuth(ctx, args[1:], stdout, stderr)
	}
	return RunServer(ctx, args, stdout, stderr)
}

func RunServer(ctx context.Context, args []string, _ io.Writer, stderr io.Writer) error {
	logger := observability.New(observability.ComponentServer, stderr)
	lifecycleLogger := observability.CategoryLogger(logger, observability.ComponentServer, observability.CategoryLifecycle)
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("sumi-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	dataRoot := flags.String("data-root", defaultRoot, "Sumi data root")
	webRoot := flags.String("web-root", "web/dist", "Production Web root")
	ownerKeyFile := flags.String("owner-key-file", "", "0600 bootstrap owner credential file")
	browserOrigin := flags.String("browser-origin", "", "loopback browser origin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	layout, err := home.Ensure(*dataRoot)
	if err != nil {
		return err
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		return err
	}
	lease, err := lifecycle.AcquireRun(layout.Root, userLayout.Runtime, lifecycle.ComponentServer)
	if err != nil {
		return err
	}
	defer lease.Close()
	lifecycleLogger.Info("server runtime lease acquired", "event", "server.lease.acquired")
	resolvedOrigin, err := resolveBrowserOrigin(*listen, *browserOrigin)
	if err != nil {
		return err
	}
	explicitOwnerKey := false
	flags.Visit(func(visited *flag.Flag) {
		if visited.Name == "owner-key-file" {
			explicitOwnerKey = true
		}
	})
	credentialPath := *ownerKeyFile
	if !explicitOwnerKey {
		credentialPath = userLayout.HumanCredential
	}
	lifecycleLogger.Info("server starting", "event", "server.starting", "listen", *listen, "browser_enabled", resolvedOrigin != "", "web_enabled", *webRoot != "")
	app, err := server.New(ctx, server.Config{
		DataRoot: layout.Root, WebRoot: *webRoot, BootstrapCredentialFile: credentialPath, BrowserOrigin: resolvedOrigin, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("initialize server: %w", err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			lifecycleLogger.Error("server resources failed to close", "event", "server.close.failed", "err", err)
			return
		}
		lifecycleLogger.Info("server stopped", "event", "server.stopped")
	}()

	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		lifecycleLogger.Info("server listening", "event", "server.listening", "listen", *listen)
		serveErrors <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		lifecycleLogger.Info("server shutdown requested", "event", "server.shutdown.requested", "reason", context.Cause(ctx))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			lifecycleLogger.Error("server graceful shutdown failed", "event", "server.shutdown.failed", "err", err)
			return err
		}
		lifecycleLogger.Info("server graceful shutdown completed", "event", "server.shutdown.completed")
		return nil
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		lifecycleLogger.Error("server listener stopped unexpectedly", "event", "server.serve.failed", "err", err)
		return err
	}
}

func RunAuth(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sumi-server auth", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverURL := flags.String("server", "http://127.0.0.1:8080", "loopback Sumi Server origin")
	serverPin := flags.String("server-pin", "", "sha256/base64url Server SPKI pin")
	humanKeyFile := flags.String("human-key-file", "", "0600 Human credential file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *humanKeyFile == "" {
		return errors.New("auth requires --human-key-file and no positional arguments")
	}
	serverEndpoint, err := endpoint.Parse(*serverURL, *serverPin)
	if err != nil {
		return errors.New("auth Server endpoint is unsafe")
	}
	handoff, err := RequestBrowserHandoff(ctx, serverEndpoint, *humanKeyFile)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, handoff.URL.String())
	return err
}

func resolveBrowserOrigin(listen, explicit string) (string, error) {
	if explicit != "" {
		if err := authority.ValidateBrowserOrigin(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", nil
	}
	return "http://" + net.JoinHostPort(host, port), nil
}
