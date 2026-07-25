package authority

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestInterceptorBrowserSessionRequiresActiveHuman(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	password := "correct-horse-battery-staple"
	digest, err := localauth.HashPassword(rand.Reader, password, localauth.DefaultPasswordParameters())
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"
	bootstrap, err := db.RegisterFirstOwner(context.Background(), authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity: authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password: digest, SessionToken: sessionToken, Now: now, SessionExpiresAt: now.Add(12 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	interceptor := NewBrowserInterceptor(db, BrowserInterceptorConfig{Origin: "http://127.0.0.1:8080"})
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
	request.Header().Set("Origin", "http://127.0.0.1:8080")
	request.Header().Set("Cookie", "sumi_browser_session="+sessionToken)
	ctx := browserContext(t, "http://127.0.0.1:8080", true)
	if _, err := wrapped(ctx, request); err != nil {
		t.Fatal(err)
	}

	for _, header := range []string{"", "invalid-token", "sumi_browser_session=wrong-token-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		request := connect.NewRequest(&emptypb.Empty{})
		request.Header().Set("Origin", "http://127.0.0.1:8080")
		request.Header().Set("Cookie", header)
		_, err := wrapped(browserContext(t, "http://127.0.0.1:8080", true), request)
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
			t.Fatalf("header %q error = %v", header, err)
		}
		if err != nil && strings.Contains(err.Error(), sessionToken) {
			t.Fatalf("session token leaked in error: %v", err)
		}
	}
}
