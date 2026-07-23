package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/server"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

var finalizeCredentialMigration = func(migration *authority.CredentialMigration) error {
	return migration.Finalize()
}

func RunContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "auth" {
		return RunAuth(ctx, args[1:], stdout, stderr)
	}
	return RunServer(ctx, args, stdout, stderr)
}

func RunServer(ctx context.Context, args []string, _ io.Writer, stderr io.Writer) error {
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
	var migration *authority.CredentialMigration
	if !explicitOwnerKey {
		migration, err = authority.PrepareCredentialMigration(layout.BootstrapCredential, userLayout.HumanCredential)
		if err != nil {
			return err
		}
		defer migration.Close()
		credentialPath = migration.CredentialPath
	}
	app, err := server.New(ctx, server.Config{
		DataRoot: layout.Root, WebRoot: *webRoot, BootstrapCredentialFile: credentialPath, BrowserOrigin: resolvedOrigin,
	})
	if err != nil {
		return fmt.Errorf("initialize server: %w", err)
	}
	if migration != nil {
		if err := finalizeCredentialMigration(migration); err != nil {
			_ = app.Close()
			return err
		}
	}
	defer app.Close()

	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	logger := log.New(stderr, "", log.LstdFlags)
	go func() {
		logger.Printf("Sumi Server listening on http://%s", *listen)
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
