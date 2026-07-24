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

type svcCtrl interface {
	Configure(string)
	Start(context.Context, osservice.Component) error
	Running(context.Context, osservice.Component) bool
}

type httpClient interface {
	Do(*http.Request) (*http.Response, error)
}

type handoffFn func(context.Context, endpoint.Endpoint, string) (serverapp.BrowserHandoff, error)

type config struct {
	dataRoot       string
	credentialFile string
	endpoint       endpoint.Endpoint
	services       svcCtrl
	client         httpClient
	requestHandoff handoffFn
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

func newApp(shell Shell, cfg config) (*App, error) {
	if shell == nil || cfg.services == nil || cfg.client == nil || cfg.requestHandoff == nil ||
		cfg.dataRoot == "" || cfg.credentialFile == "" || cfg.startupTimeout <= 0 || cfg.pollInterval <= 0 ||
		cfg.endpoint.Identity.Kind != endpoint.IdentityLiteralLoopback {
		return nil, errors.New("desktop configuration is invalid")
	}
	return &App{shell: shell, config: cfg}, nil
}

func (app *App) DomReady(ctx context.Context) {
	app.mu.Lock()
	state := app.state
	switch state {
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
		script, err := navPolicyScript(app.config.endpoint.Origin)
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
	if err := app.ensureSvc(ctx, osservice.Server); err != nil {
		return err
	}
	if err := app.waitForServer(ctx); err != nil {
		return err
	}
	if err := app.ensureSvc(ctx, osservice.Computer); err != nil {
		return err
	}
	handoff, err := app.config.requestHandoff(ctx, app.config.endpoint, app.config.credentialFile)
	if err != nil {
		return errors.New("request Desktop browser handoff")
	}
	if err := validateHandoff(app.config.endpoint, handoff); err != nil {
		return err
	}
	script, err := locationReplaceScript(handoff.URL.String())
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

func (app *App) ensureSvc(ctx context.Context, component osservice.Component) error {
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
		req, err := http.NewRequestWithContext(waitCtx, http.MethodGet, app.config.endpoint.Origin+"/healthz", nil)
		if err != nil {
			return errors.New("create Desktop health request")
		}
		resp, err := app.config.client.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
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

func validateHandoff(ep endpoint.Endpoint, handoff serverapp.BrowserHandoff) error {
	u := handoff.URL
	if u == nil || handoff.ExpiresAt.IsZero() || u.Scheme+"://"+u.Host != ep.Origin ||
		u.RawQuery != "" || u.Fragment != "" || u.EscapedPath() != u.Path {
		return errors.New("desktop browser handoff is unsafe")
	}
	token, found := strings.CutPrefix(u.Path, websession.CreateHandoffPath+"/")
	if !found || len(token) != 43 || strings.Contains(token, "/") {
		return errors.New("desktop browser handoff is unsafe")
	}
	for _, r := range token {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return errors.New("desktop browser handoff is unsafe")
		}
	}
	if u.User != nil {
		return errors.New("desktop browser handoff is unsafe")
	}
	return nil
}

func locationReplaceScript(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("desktop navigation URL is invalid")
	}
	encoded, err := json.Marshal(u.String())
	if err != nil {
		return "", errors.New("encode Desktop navigation URL")
	}
	return fmt.Sprintf("window.location.replace(%s);", encoded), nil
}

func navPolicyScript(rawOrigin string) (string, error) {
	ep, err := endpoint.Parse(rawOrigin, "")
	if err != nil || ep.Identity.Kind != endpoint.IdentityLiteralLoopback || ep.Origin != rawOrigin {
		return "", errors.New("desktop navigation origin is invalid")
	}
	encoded, err := json.Marshal(ep.Origin)
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
