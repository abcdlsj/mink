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
	} {
		t.Run(name, func(t *testing.T) {
			authenticator := &testAuthenticator{human: human, agent: agent, browser: human}
			if name == "multiple authorization" {
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
	cookieHeader := func() http.Header {
		return http.Header{"Cookie": {authority.BrowserSessionCookieName + "=" + token}}
	}

	t.Run("browser mutation origin is exact and singular", func(t *testing.T) {
		cases := map[string]http.Header{
			"missing":           cookieHeader(),
			"wrong":             cookieHeader(),
			"multiple":          cookieHeader(),
			"exactly one valid": cookieHeader(),
		}
		cases["wrong"].Set("Origin", "http://localhost:18080")
		cases["multiple"].Add("Origin", "http://127.0.0.1:18080")
		cases["multiple"].Add("Origin", "http://localhost:18080")
		cases["exactly one valid"].Set("Origin", "http://127.0.0.1:18080")
		for name, header := range cases {
			t.Run(name, func(t *testing.T) {
				resolved, err := Resolve(ctx, &testAuthenticator{browser: human}, header, true, "http://127.0.0.1:18080", now)
				if name != "exactly one valid" {
					if !errors.Is(err, ErrSameOrigin) {
						t.Fatalf("mutation origin error = %v, want ErrSameOrigin", err)
					}
					return
				}
				if got, ok := resolved.Human(); err != nil || !ok || got != human {
					t.Fatalf("browser result = %+v, %v", resolved, err)
				}
			})
		}
	})

	t.Run("browser cookie cardinality", func(t *testing.T) {
		resolved, err := Resolve(ctx, &testAuthenticator{browser: human}, cookieHeader(), false, "http://127.0.0.1:18080", now)
		if got, ok := resolved.Human(); err != nil || !ok || got != human {
			t.Fatalf("single cookie result = %+v, %v", resolved, err)
		}
		multiple := cookieHeader()
		multiple.Add("Cookie", authority.BrowserSessionCookieName+"="+token)
		if _, err := Resolve(ctx, &testAuthenticator{browser: human}, multiple, false, "http://127.0.0.1:18080", now); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("multiple cookie error = %v, want ErrUnauthenticated", err)
		}
	})

	backend := errors.New("sqlite /private/path leaked-secret credential=" + token)
	t.Run("runtime backend failure", func(t *testing.T) {
		_, err := Resolve(context.Background(), &testAuthenticator{runtimeErr: backend}, http.Header{"Authorization": {"Bearer " + token}}, false, "", now)
		assertUnavailableQuiet(t, err, token)
	})
	t.Run("human backend failure", func(t *testing.T) {
		_, err := Resolve(context.Background(), &testAuthenticator{humanErr: backend}, http.Header{"Authorization": {"Bearer " + token}}, false, "", now)
		assertUnavailableQuiet(t, err, token)
	})
	t.Run("browser backend failure", func(t *testing.T) {
		_, err := Resolve(ctx, &testAuthenticator{browserErr: backend}, cookieHeader(), false, "http://127.0.0.1:18080", now)
		assertUnavailableQuiet(t, err, token)
	})
}

type testAuthenticator struct {
	human      authoritydomain.Principal
	agent      authorityapp.RuntimeAuthentication
	browser    authoritydomain.Principal
	humanErr   error
	runtimeErr error
	browserErr error
}

func (a *testAuthenticator) AuthenticateHuman(context.Context, string) (authoritydomain.Principal, error) {
	if a.humanErr != nil {
		return authoritydomain.Principal{}, a.humanErr
	}
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
	if a.browserErr != nil {
		return authoritydomain.Principal{}, a.browserErr
	}
	if a.browser.ID == "" {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	return a.browser, nil
}

var _ Authenticator = (*testAuthenticator)(nil)

func assertUnavailableQuiet(t *testing.T, err error, credential string) {
	t.Helper()
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), credential) {
		t.Fatalf("backend error = %v", err)
	}
}

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
