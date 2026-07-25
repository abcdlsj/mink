package authority

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testBrowserAuthenticator struct {
	token     string
	principal authoritydomain.Principal
}

func (a testBrowserAuthenticator) AuthenticateBrowserSession(_ context.Context, token string, _ time.Time) (authoritydomain.Principal, error) {
	if token != a.token {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	return a.principal, nil
}

func TestBrowserInterceptorUsesBearerWithoutCookieFallback(t *testing.T) {
	principal := authoritydomain.Principal{Kind: authoritydomain.PrincipalHuman, ID: "11111111-1111-4111-8111-111111111111", OrganizationID: "22222222-2222-4222-8222-222222222222"}
	sessions := testBrowserAuthenticator{token: "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", principal: principal}
	interceptor := NewBrowserInterceptor(sessions, BrowserInterceptorConfig{
		Origin: "http://127.0.0.1:8080", BrowserReadProcedures: []string{""},
	})
	wrapper := interceptor.WrapUnary(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		subject, err := Subject(ctx)
		if err != nil {
			return nil, err
		}
		if subject != principal {
			t.Fatalf("subject = %+v", subject)
		}
		return connect.NewResponse(&emptypb.Empty{}), nil
	})

	request := connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("Cookie", BrowserSessionCookieName+"="+sessions.token)
	if _, err := wrapper(browserContext(t, "http://127.0.0.1:8080", true), request); err != nil {
		t.Fatal(err)
	}

	request = connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("Cookie", BrowserSessionCookieName+"="+sessions.token)
	request.Header().Set("Authorization", "Bearer invalid-credential-abcdefghijklmnopqrstuvwxyz")
	_, err := wrapper(browserContext(t, "http://127.0.0.1:8080", true), request)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("invalid bearer with valid cookie error = %v", err)
	}

	request = connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("Cookie", BrowserSessionCookieName+"="+sessions.token)
	if _, err := wrapper(browserContext(t, "http://127.0.0.1:8080", false), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("non-loopback cookie error = %v", err)
	}

	request = connect.NewRequest(&emptypb.Empty{})
	request.Header().Add("Cookie", BrowserSessionCookieName+"="+sessions.token)
	request.Header().Add("Cookie", BrowserSessionCookieName+"=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if _, err := wrapper(browserContext(t, "http://127.0.0.1:8080", true), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("duplicate cookie error = %v", err)
	}
}

func TestBrowserInterceptorRequiresExactOriginForMutations(t *testing.T) {
	principal := authoritydomain.Principal{Kind: authoritydomain.PrincipalHuman, ID: "11111111-1111-4111-8111-111111111111", OrganizationID: "22222222-2222-4222-8222-222222222222"}
	sessions := testBrowserAuthenticator{token: "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", principal: principal}
	interceptor := NewBrowserInterceptor(sessions, BrowserInterceptorConfig{Origin: "http://127.0.0.1:8080"})
	wrapper := interceptor.WrapUnary(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	})
	for _, origin := range []string{"", "http://localhost:8080", "http://127.0.0.1:8080/"} {
		request := connect.NewRequest(&emptypb.Empty{})
		request.Header().Set("Cookie", BrowserSessionCookieName+"="+sessions.token)
		if origin != "" {
			request.Header().Set("Origin", origin)
		}
		_, err := wrapper(browserContext(t, "http://127.0.0.1:8080", true), request)
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("origin %q error = %v", origin, err)
		}
	}
	request := connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("Cookie", BrowserSessionCookieName+"="+sessions.token)
	request.Header().Set("Origin", "http://127.0.0.1:8080")
	if _, err := wrapper(browserContext(t, "http://127.0.0.1:8080", true), request); err != nil {
		t.Fatal(err)
	}

	request = connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("Authorization", "Bearer invalid-credential")
	_, err := wrapper(browserContext(t, "http://127.0.0.1:8080", true), request)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("bearer auth with no cookie = %v", err)
	}
}

func TestBrowserOriginRejectsRemoteAndAmbiguousURLs(t *testing.T) {
	for _, origin := range []string{
		"ftp://127.0.0.1:8080",
		"http://192.0.2.1:8080",
		"http://user@127.0.0.1:8080",
		"http://127.0.0.1:8080/path",
		"http://127.0.0.1:8080?query=1",
		"http://127.0.0.1:8080#fragment",
	} {
		if err := ValidateBrowserOrigin(origin); err == nil {
			t.Fatalf("origin %q accepted", origin)
		}
	}
	for _, origin := range []string{"", "http://127.0.0.1:8080", "https://[::1]:8443", "http://localhost:8080"} {
		if err := ValidateBrowserOrigin(origin); err != nil {
			t.Fatalf("origin %q rejected: %v", origin, err)
		}
	}
}

func browserContext(t *testing.T, origin string, loopback bool) context.Context {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, origin, nil)
	if loopback {
		request.RemoteAddr = "127.0.0.1:42000"
	}
	var ctx context.Context
	handler, err := BrowserRequestMiddleware(origin, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		ctx = request.Context()
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if ctx == nil {
		t.Fatal("browser middleware did not invoke handler")
	}
	return ctx
}
