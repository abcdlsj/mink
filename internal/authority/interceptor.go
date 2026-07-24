package authority

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type subjectKey struct{}

type browserSessionAuthenticator interface {
	AuthenticateBrowserSession(context.Context, string, time.Time) (authoritydomain.Principal, error)
}

type BrowserInterceptorConfig struct {
	Origin                string
	ProtectedProcedures   []string
	BrowserReadProcedures []string
	Now                   func() time.Time
}

const BrowserSessionCookieName = "sumi_browser_session"

var browserSessionPattern = func() func(string) bool {
	return func(s string) bool {
		if len(s) != 43 {
			return false
		}
		for _, r := range s {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
				return false
			}
		}
		return true
	}
}()

func NewBrowserInterceptor(sessions browserSessionAuthenticator, config BrowserInterceptorConfig) connect.Interceptor {
	protected := procedureSet(config.ProtectedProcedures)
	if len(config.ProtectedProcedures) == 0 {
		protected = nil
	}
	return newInterceptor(protected, config, sessions)
}

func newInterceptor(protected map[string]struct{}, config BrowserInterceptorConfig, sessions browserSessionAuthenticator) connect.Interceptor {
	reads := procedureSet(config.BrowserReadProcedures)
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if protected != nil {
				if _, ok := protected[req.Spec().Procedure]; !ok {
					return next(ctx, req)
				}
			}
			subject, err := authenticateRequest(ctx, req, sessions, config.Origin, reads, now())
			if errors.Is(err, authoritydomain.ErrPermissionDenied) {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("human authentication invalid"))
			}
			if err != nil {
				var connectErr *connect.Error
				if errors.As(err, &connectErr) {
					return nil, connectErr
				}
				return nil, connect.NewError(connect.CodeInternal, errors.New("authenticate human"))
			}
			return next(context.WithValue(ctx, subjectKey{}, subject), req)
		}
	})
}

func authenticateRequest(ctx context.Context, req connect.AnyRequest, sessions browserSessionAuthenticator, origin string, browserReads map[string]struct{}, now time.Time) (authoritydomain.Principal, error) {
	if len(req.Header().Values("Authorization")) > 0 {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	if sessions == nil || origin == "" || !BrowserRequestAllowed(ctx) {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	if _, read := browserReads[req.Spec().Procedure]; !read {
		origins := req.Header().Values("Origin")
		if len(origins) != 1 || origins[0] != origin {
			return authoritydomain.Principal{}, connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
		}
	}
	token, ok := browserSessionCookie(req.Header())
	if !ok {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	return sessions.AuthenticateBrowserSession(ctx, token, now)
}

func BearerCredential(header http.Header) (string, bool) {
	authorization := header.Values("Authorization")
	if len(authorization) != 1 {
		return "", false
	}
	return bearerCredential(authorization[0])
}

func browserSessionCookie(header http.Header) (string, bool) {
	req := http.Request{Header: header}
	cookies := req.CookiesNamed(BrowserSessionCookieName)
	if len(cookies) != 1 || !browserSessionPattern(cookies[0].Value) {
		return "", false
	}
	return cookies[0].Value, true
}

func procedureSet(procedures []string) map[string]struct{} {
	result := make(map[string]struct{}, len(procedures))
	for _, procedure := range procedures {
		result[procedure] = struct{}{}
	}
	return result
}

func Subject(ctx context.Context) (authoritydomain.Principal, error) {
	subject, ok := ctx.Value(subjectKey{}).(authoritydomain.Principal)
	if !ok || subject.Kind != authoritydomain.PrincipalHuman || !subject.Valid() {
		return authoritydomain.Principal{}, connect.NewError(connect.CodeUnauthenticated, errors.New("authenticated human required"))
	}
	return subject, nil
}

func bearerCredential(header string) (string, bool) {
	prefix, credential, ok := strings.Cut(header, " ")
	if !ok || prefix != "Bearer" || !validBearerToken(credential) || strings.Contains(credential, " ") {
		return "", false
	}
	return credential, true
}

func validBearerToken(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
