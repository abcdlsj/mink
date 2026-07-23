package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/endpoint"
	"github.com/abcdlsj/sumi/internal/home"
	"github.com/abcdlsj/sumi/internal/osservice"
	serverapp "github.com/abcdlsj/sumi/internal/server/app"
	"github.com/abcdlsj/sumi/internal/userdirs"
)

const DefaultOrigin = "http://127.0.0.1:8080"

type Shell interface {
	ExecJS(context.Context, string)
}

type serviceController interface {
	Configure(string)
	Start(context.Context, osservice.Component) error
	Running(context.Context, osservice.Component) bool
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type handoffRequester func(context.Context, endpoint.Endpoint, string) (serverapp.BrowserHandoff, error)

type config struct {
	dataRoot       string
	credentialFile string
	endpoint       endpoint.Endpoint
	services       serviceController
	client         httpDoer
	requestHandoff handoffRequester
	startupTimeout time.Duration
	pollInterval   time.Duration
}

type appState uint8

const (
	stateBootstrap appState = iota
	stateOpening
	stateNavigated
	stateFailed
)

type App struct {
	shell  Shell
	config config

	mu    sync.Mutex
	state appState
}

func New(shell Shell) (*App, error) {
	if shell == nil {
		return nil, errors.New("desktop shell is unavailable")
	}
	dataRoot, err := home.DefaultRoot()
	if err != nil {
		return nil, errors.New("resolve Desktop data root")
	}
	userLayout, err := userdirs.Ensure()
	if err != nil {
		return nil, errors.New("resolve Desktop user state")
	}
	serverEndpoint, err := endpoint.Parse(DefaultOrigin, "")
	if err != nil {
		return nil, errors.New("resolve Desktop Server endpoint")
	}
	services, err := osservice.New()
	if err != nil {
		return nil, errors.New("create Desktop service controller")
	}
	services.Configure(dataRoot)
	client, err := endpoint.NewHTTPClient(serverEndpoint)
	if err != nil {
		return nil, errors.New("create Desktop Server transport")
	}
	return newApp(shell, config{
		dataRoot: dataRoot, credentialFile: userLayout.HumanCredential,
		endpoint: serverEndpoint, services: services, client: client,
		requestHandoff: serverapp.RequestBrowserHandoff,
		startupTimeout: 15 * time.Second, pollInterval: 50 * time.Millisecond,
	})
}

func newApp(shell Shell, config config) (*App, error) {
	if shell == nil || config.services == nil || config.client == nil || config.requestHandoff == nil ||
		config.dataRoot == "" || config.credentialFile == "" || config.startupTimeout <= 0 || config.pollInterval <= 0 ||
		config.endpoint.Identity.Kind != endpoint.IdentityLiteralLoopback {
		return nil, errors.New("desktop configuration is invalid")
	}
	return &App{shell: shell, config: config}, nil
}

func (app *App) DomReady(ctx context.Context) {
	app.mu.Lock()
	switch app.state {
	case stateBootstrap:
		app.state = stateOpening
		app.mu.Unlock()
		if err := app.open(ctx); err != nil {
			app.fail(ctx)
		}
	case stateOpening:
		app.mu.Unlock()
	case stateNavigated:
		app.mu.Unlock()
		script, err := NavigationPolicyScript(app.config.endpoint.Origin)
		if err != nil {
			app.fail(ctx)
			return
		}
		app.shell.ExecJS(ctx, script)
	case stateFailed:
		app.mu.Unlock()
		app.shell.ExecJS(ctx, StartupFailureScript())
	default:
		app.mu.Unlock()
		app.fail(ctx)
	}
}

func (app *App) open(ctx context.Context) error {
	if err := app.ensureService(ctx, osservice.Server); err != nil {
		return err
	}
	if err := app.waitForServer(ctx); err != nil {
		return err
	}
	if err := app.ensureService(ctx, osservice.Computer); err != nil {
		return err
	}
	handoff, err := app.config.requestHandoff(ctx, app.config.endpoint, app.config.credentialFile)
	if err != nil {
		return errors.New("request Desktop browser handoff")
	}
	if err := validateHandoff(app.config.endpoint, handoff); err != nil {
		return err
	}
	script, err := LocationReplaceScript(handoff.URL.String())
	if err != nil {
		return err
	}
	app.mu.Lock()
	if app.state != stateOpening {
		app.mu.Unlock()
		return errors.New("desktop startup state changed")
	}
	app.state = stateNavigated
	app.mu.Unlock()
	app.shell.ExecJS(ctx, script)
	return nil
}

func (app *App) ensureService(ctx context.Context, component osservice.Component) error {
	if app.config.services.Running(ctx, component) {
		return nil
	}
	if err := app.config.services.Start(ctx, component); err != nil {
		return errors.New("start Desktop dependency")
	}
	return nil
}

func (app *App) waitForServer(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, app.config.startupTimeout)
	defer cancel()
	ticker := time.NewTicker(app.config.pollInterval)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(waitCtx, http.MethodGet, app.config.endpoint.Origin+"/healthz", nil)
		if err != nil {
			return errors.New("create Desktop health request")
		}
		response, err := app.config.client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return nil
			}
		}
		select {
		case <-waitCtx.Done():
			return errors.New("desktop Server did not become healthy")
		case <-ticker.C:
		}
	}
}

func (app *App) fail(ctx context.Context) {
	app.mu.Lock()
	app.state = stateFailed
	app.mu.Unlock()
	app.shell.ExecJS(ctx, StartupFailureScript())
}

func validateHandoff(serverEndpoint endpoint.Endpoint, handoff serverapp.BrowserHandoff) error {
	if handoff.URL == nil || handoff.ExpiresAt.IsZero() || handoff.URL.Scheme+"://"+handoff.URL.Host != serverEndpoint.Origin ||
		handoff.URL.RawQuery != "" || handoff.URL.Fragment != "" || handoff.URL.EscapedPath() != handoff.URL.Path {
		return errors.New("desktop browser handoff is unsafe")
	}
	token, found := strings.CutPrefix(handoff.URL.Path, websession.CreateHandoffPath+"/")
	if !found || len(token) != 43 || strings.Contains(token, "/") {
		return errors.New("desktop browser handoff is unsafe")
	}
	for _, character := range token {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return errors.New("desktop browser handoff is unsafe")
		}
	}
	if handoff.URL.User != nil {
		return errors.New("desktop browser handoff is unsafe")
	}
	return nil
}

func LocationReplaceScript(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("desktop navigation URL is invalid")
	}
	encoded, err := json.Marshal(parsed.String())
	if err != nil {
		return "", errors.New("encode Desktop navigation URL")
	}
	return fmt.Sprintf("window.location.replace(%s);", encoded), nil
}

func NavigationPolicyScript(rawOrigin string) (string, error) {
	serverEndpoint, err := endpoint.Parse(rawOrigin, "")
	if err != nil || serverEndpoint.Identity.Kind != endpoint.IdentityLiteralLoopback || serverEndpoint.Origin != rawOrigin {
		return "", errors.New("desktop navigation origin is invalid")
	}
	encoded, err := json.Marshal(serverEndpoint.Origin)
	if err != nil {
		return "", errors.New("encode Desktop navigation origin")
	}
	return fmt.Sprintf(`(() => {
  const expectedOrigin = %s;
  if (window.location.origin !== expectedOrigin) {
    window.stop();
    window.location.replace(expectedOrigin + "/");
    return;
  }
  if (window.__sumiDesktopNavigationPolicyInstalled) return;
  Object.defineProperty(window, "__sumiDesktopNavigationPolicyInstalled", {value: true});
  document.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest("a[href]") : null;
    if (!target) return;
    const destination = new URL(target.href, window.location.href);
    if (destination.origin === expectedOrigin) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (destination.protocol === "http:" || destination.protocol === "https:") {
      const message = "BO:" + destination.href;
      if (window.chrome?.webview?.postMessage) {
        window.chrome.webview.postMessage(message);
      } else if (window.webkit?.messageHandlers?.external?.postMessage) {
        window.webkit.messageHandlers.external.postMessage(message);
      }
    }
  }, true);
})();`, encoded), nil
}

func StartupFailureScript() string {
	return `document.body.replaceChildren(Object.assign(document.createElement("main"), {textContent: "Sumi could not start."}));`
}
