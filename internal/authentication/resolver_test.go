package authentication

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

func TestResolveEnforcesExactlyOneCredentialIdentity(t *testing.T) {
	token := strings.Repeat("a", 43)
	human := authoritydomain.Principal{Kind: authoritydomain.PrincipalHuman, ID: "human-id", OrganizationID: "organization-id"}
	agent := authorityapp.RuntimeAuthentication{Principal: authoritydomain.Principal{Kind: authoritydomain.PrincipalAgent, ID: "agent-id", OrganizationID: "organization-id"}}
	now := time.Date(2026, 7, 23, 4, 0, 0, 0, time.UTC)

	t.Run("human bearer", func(t *testing.T) {
		resolved, err := Resolve(context.Background(), &testAuthenticator{human: human}, http.Header{"Authorization": {"Bearer " + token}}, false, "", now)
		got, humanOK := resolved.Human()
		if err != nil || !humanOK || got != human {
			t.Fatalf("human bearer = %+v, %v", resolved, err)
		}
		if _, agentOK := resolved.Agent(); agentOK {
			t.Fatal("human result also exposed agent")
		}
	})

	t.Run("runtime bearer", func(t *testing.T) {
		resolved, err := Resolve(context.Background(), &testAuthenticator{agent: agent}, http.Header{"Authorization": {"Bearer " + token}}, false, "", now)
		got, agentOK := resolved.Agent()
		if err != nil || !agentOK || got != agent {
			t.Fatalf("runtime bearer = %+v, %v", resolved, err)
		}
		if _, humanOK := resolved.Human(); humanOK {
			t.Fatal("agent result also exposed human")
		}
	})

	for name, header := range map[string]http.Header{
		"ambiguous bearer":       {"Authorization": {"Bearer " + token}},
		"bearer and cookie":      {"Authorization": {"Bearer " + token}, "Cookie": {authority.BrowserSessionCookieName + "=" + token}},
		"multiple authorization": {"Authorization": {"Bearer " + token, "Bearer " + token}},
		"multiple cookie":        {"Cookie": {authority.BrowserSessionCookieName + "=" + token, authority.BrowserSessionCookieName + "=" + token}},
	} {
		t.Run(name, func(t *testing.T) {
			authenticator := &testAuthenticator{human: human, agent: agent, browser: human}
			if name == "multiple authorization" || name == "multiple cookie" {
				authenticator.agent = authorityapp.RuntimeAuthentication{}
			}
			_, err := Resolve(context.Background(), authenticator, header, false, "http://127.0.0.1:18080", now)
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("Resolve error = %v, want ErrUnauthenticated", err)
			}
		})
	}
}

func TestResolveBrowserOriginAndBackendFailuresAreQuiet(t *testing.T) {
	token := strings.Repeat("a", 43)
	human := authoritydomain.Principal{Kind: authoritydomain.PrincipalHuman, ID: "human-id", OrganizationID: "organization-id"}
	now := time.Date(2026, 7, 23, 4, 0, 0, 0, time.UTC)
	ctx := browserContext(t, "http://127.0.0.1:18080")
	header := http.Header{"Cookie": {authority.BrowserSessionCookieName + "=" + token}}

	if _, err := Resolve(ctx, &testAuthenticator{browser: human}, header, true, "http://127.0.0.1:18080", now); !errors.Is(err, ErrSameOrigin) {
		t.Fatalf("missing origin = %v, want ErrSameOrigin", err)
	}
	header.Set("Origin", "http://127.0.0.1:18080")
	resolved, err := Resolve(ctx, &testAuthenticator{browser: human}, header, true, "http://127.0.0.1:18080", now)
	if got, ok := resolved.Human(); err != nil || !ok || got != human {
		t.Fatalf("browser result = %+v, %v", resolved, err)
	}

	backend := errors.New("sqlite /private/path leaked-secret")
	_, err = Resolve(context.Background(), &testAuthenticator{runtimeErr: backend}, http.Header{"Authorization": {"Bearer " + token}}, false, "", now)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("backend error = %v", err)
	}
}

type testAuthenticator struct {
	human      authoritydomain.Principal
	agent      authorityapp.RuntimeAuthentication
	browser    authoritydomain.Principal
	runtimeErr error
}

func (a *testAuthenticator) AuthenticateHuman(context.Context, string) (authoritydomain.Principal, error) {
	if a.human.ID == "" {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	return a.human, nil
}

func (a *testAuthenticator) AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (authorityapp.RuntimeAuthentication, error) {
	if a.runtimeErr != nil {
		return authorityapp.RuntimeAuthentication{}, a.runtimeErr
	}
	if a.agent.Principal.ID == "" {
		return authorityapp.RuntimeAuthentication{}, authorityapp.ErrRuntimeUnauthenticated
	}
	return a.agent, nil
}

func (a *testAuthenticator) AuthenticateBrowserSession(context.Context, string, time.Time) (authoritydomain.Principal, error) {
	if a.browser.ID == "" {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	return a.browser, nil
}

var _ Authenticator = (*testAuthenticator)(nil)

func browserContext(t *testing.T, origin string) context.Context {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, origin, nil)
	request.RemoteAddr = "127.0.0.1:42000"
	var result context.Context
	handler, err := authority.BrowserRequestMiddleware(origin, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		result = request.Context()
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)
	return result
}
