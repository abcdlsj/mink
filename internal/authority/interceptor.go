package authority

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
)

type subjectKey struct{}

type humanAuthenticator interface {
	AuthenticateHuman(context.Context, string) (store.Principal, error)
}

func NewInterceptor(authenticator humanAuthenticator) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			credential, ok := bearerCredential(request.Header().Get("Authorization"))
			if !ok {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("human credential required"))
			}
			subject, err := authenticator.AuthenticateHuman(ctx, credential)
			if errors.Is(err, store.ErrPermissionDenied) {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("human credential invalid"))
			}
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("authenticate human"))
			}
			return next(context.WithValue(ctx, subjectKey{}, subject), request)
		}
	})
}

func Subject(ctx context.Context) (store.Principal, error) {
	subject, ok := ctx.Value(subjectKey{}).(store.Principal)
	if !ok || subject.Kind != "human" || subject.ID == "" || subject.OrganizationID == "" {
		return store.Principal{}, connect.NewError(connect.CodeUnauthenticated, errors.New("authenticated human required"))
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
