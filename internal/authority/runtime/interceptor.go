package runtime

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type subjectKey struct{}

type sessionAuthenticator interface {
	AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (authorityapp.RuntimeAuthentication, error)
}

func validRuntimeToken(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func NewProcedureInterceptor(authenticator sessionAuthenticator, procedures ...string) connect.Interceptor {
	return newProcInterceptor(authenticator, time.Now, procedures...)
}

func newProcInterceptor(authenticator sessionAuthenticator, now func() time.Time, procedures ...string) connect.Interceptor {
	protected := make(map[string]struct{}, len(procedures))
	for _, p := range procedures {
		protected[p] = struct{}{}
	}
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if _, ok := protected[req.Spec().Procedure]; !ok {
				return next(ctx, req)
			}
			token, ok := bearerToken(req.Header().Values("Authorization"))
			if !ok {
				return nil, unauth()
			}
			auth, err := authenticator.AuthenticateAgentRuntimeSession(ctx, token, now())
			if errors.Is(err, authorityapp.ErrRuntimeUnauthenticated) {
				return nil, unauth()
			}
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("authenticate agent runtime session"))
			}
			return next(context.WithValue(ctx, subjectKey{}, auth), req)
		}
	})
}

func Subject(ctx context.Context) (authoritydomain.Principal, authorityapp.RuntimeProof, error) {
	a, ok := ctx.Value(subjectKey{}).(authorityapp.RuntimeAuthentication)
	if !ok || !a.Valid() {
		return authoritydomain.Principal{}, authorityapp.RuntimeProof{}, unauth()
	}
	return a.Principal, a.Proof, nil
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	prefix, token, ok := strings.Cut(values[0], " ")
	if !ok || prefix != "Bearer" || !validRuntimeToken(token) {
		return "", false
	}
	return token, true
}

func unauth() error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
}
