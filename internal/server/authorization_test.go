package server

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/authority"
)

func ownerClientAuthorization(t *testing.T, dataRoot string) connect.Option {
	t.Helper()
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	return clientAuthorization(credential)
}

func clientAuthorization(credential string) connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+credential)
			return next(ctx, request)
		}
	}))
}
