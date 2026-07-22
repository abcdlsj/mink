package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/server"
)

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
	resolvedOrigin, err := resolveBrowserOrigin(*listen, *browserOrigin)
	if err != nil {
		return err
	}
	app, err := server.New(ctx, server.Config{
		DataRoot: *dataRoot, WebRoot: *webRoot, BootstrapCredentialFile: *ownerKeyFile, BrowserOrigin: resolvedOrigin,
	})
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
	humanKeyFile := flags.String("human-key-file", "", "0600 Human credential file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *humanKeyFile == "" {
		return errors.New("auth requires --human-key-file and no positional arguments")
	}
	origin, err := parseLoopbackOrigin(*serverURL)
	if err != nil {
		return errors.New("auth server must be a loopback HTTP or HTTPS origin")
	}
	credential, err := authority.ReadCredentialFile(*humanKeyFile)
	if err != nil {
		return errors.New("human credential file is missing or unsafe")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin.String()+websession.CreateHandoffPath, nil)
	if err != nil {
		return errors.New("create browser authentication request")
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("browser authentication request failed")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return errors.New("browser authentication request refused a redirect")
	}
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("browser authentication request returned status %d", response.StatusCode)
	}
	var handoff struct {
		Path      string    `json:"path"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2049))
	if err != nil || len(payload) > 2048 {
		return errors.New("browser authentication response is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&handoff); err != nil {
		return errors.New("browser authentication response is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("browser authentication response is invalid")
	}
	if handoff.ExpiresAt.IsZero() {
		return errors.New("browser authentication response is invalid")
	}
	handoffURL, err := resolveHandoffURL(origin, handoff.Path)
	if err != nil {
		return errors.New("browser authentication response is unsafe")
	}
	_, err = fmt.Fprintln(stdout, handoffURL.String())
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

func parseLoopbackOrigin(raw string) (*url.URL, error) {
	if err := authority.ValidateBrowserOrigin(raw); err != nil || raw == "" {
		return nil, authority.ErrBrowserOriginInvalid
	}
	return url.Parse(raw)
}

func resolveHandoffURL(origin *url.URL, path string) (*url.URL, error) {
	handoff, err := url.Parse(path)
	if err != nil || handoff.IsAbs() || handoff.Host != "" || handoff.RawQuery != "" || handoff.Fragment != "" || !strings.HasPrefix(handoff.Path, websession.CreateHandoffPath+"/") {
		return nil, errors.New("invalid handoff path")
	}
	token := strings.TrimPrefix(handoff.Path, websession.CreateHandoffPath+"/")
	if len(token) != 43 || strings.Contains(token, "/") || handoff.EscapedPath() != handoff.Path {
		return nil, errors.New("invalid handoff token")
	}
	for _, character := range token {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return nil, errors.New("invalid handoff token")
		}
	}
	resolved := origin.ResolveReference(handoff)
	if resolved.Scheme != origin.Scheme || resolved.Host != origin.Host {
		return nil, errors.New("cross-origin handoff")
	}
	return resolved, nil
}
