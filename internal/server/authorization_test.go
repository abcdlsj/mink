package server

import (
	"context"

	"connectrpc.com/connect"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/google/uuid"
)

func ownerBrowserClient(t *testing.T, origin string) (connect.Option, string) {
	t.Helper()
	sessionToken := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP" // 43 chars
	return browserSessionAuth(sessionToken, origin), sessionToken
}

func browserSessionAuth(sessionToken, origin string) connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Cookie", "sumi_browser_session="+sessionToken)
			if origin != "" {
				request.Header().Set("Origin", origin)
			}
			return next(ctx, request)
		}
	}))
}

func registerTestOwner(t *testing.T) (authorityapp.RegisterFirstOwnerCommand, string) {
	t.Helper()
	sessionToken := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP" // 43 chars
	cmd := authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity: authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		SessionToken: sessionToken,
	}
	return cmd, sessionToken
}
