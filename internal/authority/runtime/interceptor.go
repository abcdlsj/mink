package runtime

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
)

type subjectKey struct{}

type sessionAuthenticator interface {
	AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (store.AgentRuntimeAuthentication, error)
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
			if errors.Is(err, store.ErrAgentRuntimeUnauthenticated) {
				return nil, unauthenticated()
			}
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("authenticate agent runtime session"))
			}
			return next(context.WithValue(ctx, subjectKey{}, authentication), request)
		}
	})
}

func Subject(ctx context.Context) (store.Principal, store.AgentRuntimeProof, error) {
	authentication, ok := ctx.Value(subjectKey{}).(store.AgentRuntimeAuthentication)
	if !ok || !authentication.Valid() {
		return store.Principal{}, store.AgentRuntimeProof{}, unauthenticated()
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
