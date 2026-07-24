package authority

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestInterceptorAuthenticatesActiveHumanWithoutCredentialLeak(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	credential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	bootstrap, err := db.EnsureAuthority(context.Background(), credential, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	interceptor := NewBrowserInterceptor(db, db, BrowserInterceptorConfig{})
	wrapped := interceptor.WrapUnary(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		subject, err := Subject(ctx)
		if err != nil {
			return nil, err
		}
		if subject.ID != bootstrap.Human.ID || subject.OrganizationID != bootstrap.Organization.ID {
			t.Fatalf("subject = %+v", subject)
		}
		return connect.NewResponse(&emptypb.Empty{}), nil
	})
	request := connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("Authorization", "Bearer "+credential)
	if _, err := wrapped(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	for _, header := range []string{"", credential, "Bearer wrong-credential-abcdefghijklmnopqrstuvwxyz-012345", "bearer " + credential} {
		request := connect.NewRequest(&emptypb.Empty{})
		request.Header().Set("Authorization", header)
		_, err := wrapped(context.Background(), request)
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
			t.Fatalf("header %q error = %v", header, err)
		}
		if err != nil && strings.Contains(err.Error(), credential) {
			t.Fatalf("credential leaked in error: %v", err)
		}
	}
}
