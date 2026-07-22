package runtime

import (
	"context"
	"errors"
	"regexp"
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

var runtimeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func NewProcedureInterceptor(authenticator sessionAuthenticator, procedures ...string) connect.Interceptor {
	return newProcedureInterceptor(authenticator, time.Now, procedures...)
}

func newProcedureInterceptor(authenticator sessionAuthenticator, now func() time.Time, procedures ...string) connect.Interceptor {
	protected := make(map[string]struct{}, len(procedures))
	for _, procedure := range procedures {
		protected[procedure] = struct{}{}
	}
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			if _, ok := protected[request.Spec().Procedure]; !ok {
				return next(ctx, request)
			}
			token, ok := bearerToken(request.Header().Values("Authorization"))
			if !ok {
				return nil, unauthenticated()
			}
			authentication, err := authenticator.AuthenticateAgentRuntimeSession(ctx, token, now())
			if errors.Is(err, authorityapp.ErrRuntimeUnauthenticated) {
				return nil, unauthenticated()
			}
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("authenticate agent runtime session"))
			}
			return next(context.WithValue(ctx, subjectKey{}, authentication), request)
		}
	})
}

func Subject(ctx context.Context) (authoritydomain.Principal, authorityapp.RuntimeProof, error) {
	authentication, ok := ctx.Value(subjectKey{}).(authorityapp.RuntimeAuthentication)
	if !ok || !authentication.Valid() {
		return authoritydomain.Principal{}, authorityapp.RuntimeProof{}, unauthenticated()
	}
	return authentication.Principal, authentication.Proof, nil
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	prefix, token, ok := strings.Cut(values[0], " ")
	if !ok || prefix != "Bearer" || !runtimeTokenPattern.MatchString(token) {
		return "", false
	}
	return token, true
}

func unauthenticated() error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
}
