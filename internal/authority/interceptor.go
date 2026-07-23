package authority

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type subjectKey struct{}

type humanAuthenticator interface {
	AuthenticateHuman(context.Context, string) (authoritydomain.Principal, error)
}

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

var browserSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func NewBrowserInterceptor(authenticator humanAuthenticator, sessions browserSessionAuthenticator, config BrowserInterceptorConfig) connect.Interceptor {
	protected := procedureSet(config.ProtectedProcedures)
	if len(config.ProtectedProcedures) == 0 {
		protected = nil
	}
	return newInterceptor(authenticator, protected, config, sessions)
}

func newInterceptor(authenticator humanAuthenticator, protected map[string]struct{}, config BrowserInterceptorConfig, sessions ...browserSessionAuthenticator) connect.Interceptor {
	reads := procedureSet(config.BrowserReadProcedures)
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			if protected != nil {
				if _, ok := protected[request.Spec().Procedure]; !ok {
					return next(ctx, request)
				}
			}
			subject, err := authenticateRequest(ctx, request, authenticator, sessions, config.Origin, reads, now())
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
			return next(context.WithValue(ctx, subjectKey{}, subject), request)
		}
	})
}

func authenticateRequest(ctx context.Context, request connect.AnyRequest, authenticator humanAuthenticator, sessions []browserSessionAuthenticator, origin string, browserReads map[string]struct{}, now time.Time) (authoritydomain.Principal, error) {
	if len(request.Header().Values("Authorization")) > 0 {
		credential, ok := BearerCredential(request.Header())
		if !ok {
			return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
		}
		return authenticator.AuthenticateHuman(ctx, credential)
	}
	if len(sessions) != 1 || origin == "" || !BrowserRequestAllowed(ctx) {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	if _, read := browserReads[request.Spec().Procedure]; !read {
		origins := request.Header().Values("Origin")
		if len(origins) != 1 || origins[0] != origin {
			return authoritydomain.Principal{}, connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
		}
	}
	token, ok := browserSessionCookie(request.Header())
	if !ok {
		return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
	}
	return sessions[0].AuthenticateBrowserSession(ctx, token, now)
}

func BearerCredential(header http.Header) (string, bool) {
	authorization := header.Values("Authorization")
	if len(authorization) != 1 {
		return "", false
	}
	return bearerCredential(authorization[0])
}

func browserSessionCookie(header http.Header) (string, bool) {
	request := http.Request{Header: header}
	cookies := request.CookiesNamed(BrowserSessionCookieName)
	if len(cookies) != 1 || !browserSessionPattern.MatchString(cookies[0].Value) {
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
	if !ok || prefix != "Bearer" || !ValidCredential(credential) || strings.Contains(credential, " ") {
		return "", false
	}
	return credential, true
}
