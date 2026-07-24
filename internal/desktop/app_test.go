package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/osservice"
	serverapp "github.com/abcdlsj/sumi/internal/server/app"
)

const desktopTestToken = "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"

type recordingShell struct {
	scripts []string
}

func (shell *recordingShell) ExecJS(_ context.Context, script string) {
	shell.scripts = append(shell.scripts, script)
}

type fakeServices struct {
	running map[osservice.Component]bool
	starts  []osservice.Component
}

func (services *fakeServices) Configure(string) {}

func (services *fakeServices) Start(_ context.Context, component osservice.Component) error {
	services.starts = append(services.starts, component)
	services.running[component] = true
	return nil
}

func (services *fakeServices) Running(_ context.Context, component osservice.Component) bool {
	return services.running[component]
}

func TestDesktopStartsServicesAndRequestsFreshHandoffPerProcess(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			t.Fatalf("health path = %q", request.URL.Path)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer health.Close()
	serverEndpoint, err := endpoint.Parse(health.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	services := &fakeServices{running: map[osservice.Component]bool{}}
	requests := 0
	requestHandoff := func(_ context.Context, got endpoint.Endpoint, credentialFile string) (serverapp.BrowserHandoff, error) {
		requests++
		if got != serverEndpoint || credentialFile != "/private/human.key" {
			t.Fatalf("handoff input = %+v/%q", got, credentialFile)
		}
		handoffURL, err := urlForTest(serverEndpoint.Origin + websession.CreateHandoffPath + "/" + desktopTestToken)
		return serverapp.BrowserHandoff{URL: handoffURL, ExpiresAt: time.Now().Add(time.Minute)}, err
	}
	newProcess := func() *recordingShell {
		shell := &recordingShell{}
		app, err := newApp(shell, config{
			dataRoot: "/private/data", credentialFile: "/private/human.key", endpoint: serverEndpoint,
			services: services, client: health.Client(), requestHandoff: requestHandoff,
			startupTimeout: time.Second, pollInterval: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		app.DomReady(context.Background())
		if len(shell.scripts) != 1 || !strings.Contains(shell.scripts[0], desktopTestToken) {
			t.Fatalf("startup scripts = %#v", shell.scripts)
		}
		app.DomReady(context.Background())
		if len(shell.scripts) != 2 || !strings.Contains(shell.scripts[1], `"BO:" + destination.href`) || !strings.Contains(shell.scripts[1], "messageHandlers.external.postMessage") || strings.Contains(shell.scripts[1], desktopTestToken) {
			t.Fatalf("navigation policy = %#v", shell.scripts)
		}
		return shell
	}
	newProcess()
	newProcess()
	if requests != 2 {
		t.Fatalf("handoff requests = %d", requests)
	}
	if fmt.Sprint(services.starts) != "[server computer]" {
		t.Fatalf("service starts = %v", services.starts)
	}
}

func TestDesktopFailureIsQuietAndDoesNotRetryInProcess(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer health.Close()
	serverEndpoint, err := endpoint.Parse(health.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	shell := &recordingShell{}
	requests := 0
	app, err := newApp(shell, config{
		dataRoot: "/private/data", credentialFile: "/private/secret-human.key", endpoint: serverEndpoint,
		services: &fakeServices{running: map[osservice.Component]bool{osservice.Server: true, osservice.Computer: true}},
		client:   health.Client(), startupTimeout: time.Second, pollInterval: time.Millisecond,
		requestHandoff: func(context.Context, endpoint.Endpoint, string) (serverapp.BrowserHandoff, error) {
			requests++
			return serverapp.BrowserHandoff{}, errors.New("credential-value /private/secret-human.key")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app.DomReady(context.Background())
	app.DomReady(context.Background())
	if requests != 1 || len(shell.scripts) != 2 {
		t.Fatalf("requests/scripts = %d/%#v", requests, shell.scripts)
	}
	for _, script := range shell.scripts {
		if strings.Contains(script, "credential-value") || strings.Contains(script, "/private/secret-human.key") {
			t.Fatalf("secret leaked to shell: %q", script)
		}
	}
}

func TestNavigationPolicyRequiresExactLiteralLoopback(t *testing.T) {
	script, err := navPolicyScript("http://127.0.0.1:8080")
	if err != nil || !strings.Contains(script, `window.location.origin !== expectedOrigin`) || !strings.Contains(script, `event.stopImmediatePropagation()`) {
		t.Fatalf("policy = %q, %v", script, err)
	}
	for _, unsafe := range []string{"http://localhost:8080", "http://192.0.2.1:8080", "https://example.com"} {
		if _, err := navPolicyScript(unsafe); err == nil {
			t.Fatalf("unsafe origin accepted: %q", unsafe)
		}
	}
}

func TestValidateHandoffRejectsMalformedTokens(t *testing.T) {
	serverEndpoint, err := endpoint.Parse("http://127.0.0.1:8080", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{
		serverEndpoint.Origin + websession.CreateHandoffPath + "/short",
		serverEndpoint.Origin + websession.CreateHandoffPath + "/abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNO%2F",
		serverEndpoint.Origin + websession.CreateHandoffPath + "/abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNO!",
		serverEndpoint.Origin + websession.CreateHandoffPath + "/" + desktopTestToken + "/extra",
		serverEndpoint.Origin + websession.CreateHandoffPath + "/" + desktopTestToken + "?leak=true",
		"http://127.0.0.1:8081" + websession.CreateHandoffPath + "/" + desktopTestToken,
	} {
		handoffURL, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if err := validateHandoff(serverEndpoint, serverapp.BrowserHandoff{URL: handoffURL, ExpiresAt: time.Now().Add(time.Minute)}); err == nil {
			t.Fatalf("unsafe handoff accepted: %q", rawURL)
		}
	}
}

func urlForTest(raw string) (*url.URL, error) {
	return url.Parse(raw)
}
