package websession

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	a "github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestBrowserSessionLogoutRequiresExactOriginAndClearsCookie(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	password := "test-password-for-session-test"
	digest, err := localauth.HashPassword(rand.Reader, password, localauth.DefaultPasswordParameters())
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"
	if _, err := db.RegisterFirstOwner(context.Background(), authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity:         authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password:         digest,
		SessionToken:     sessionToken,
		Now:              now,
		SessionExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	handler := browserHandler(t, db, "http://127.0.0.1:8080", func() time.Time { return now })
	session := &http.Cookie{
		Name: a.BrowserSessionCookieName, Value: sessionToken, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Expires: now.Add(12 * time.Hour), MaxAge: 12 * 60 * 60,
	}
	duplicate := map[string]string{"Cookie": session.Name + "=" + session.Value + "; " + session.Name + "=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	status := request(t, handler, http.MethodGet, SessionPath, "127.0.0.1:42000", duplicate)
	if status.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate cookie session status = %d", status.Code)
	}
	duplicate["Origin"] = "http://127.0.0.1:8080"
	logout := request(t, handler, http.MethodPost, LogoutPath, "127.0.0.1:42000", duplicate)
	if logout.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate cookie logout = %d", logout.Code)
	}

	for _, origin := range []string{"", "http://localhost:8080", "http://127.0.0.1:8080/"} {
		headers := map[string]string{"Cookie": session.String()}
		if origin != "" {
			headers["Origin"] = origin
		}
		logout := request(t, handler, http.MethodPost, LogoutPath, "127.0.0.1:42000", headers)
		if logout.Code != http.StatusForbidden {
			t.Fatalf("origin %q logout = %d", origin, logout.Code)
		}
		status := request(t, handler, http.MethodGet, SessionPath, "127.0.0.1:42000", map[string]string{"Cookie": session.String()})
		if status.Code != http.StatusOK {
			t.Fatalf("origin %q revoked session", origin)
		}
	}
	logout = request(t, handler, http.MethodPost, LogoutPath, "127.0.0.1:42000", map[string]string{
		"Cookie": session.String(), "Origin": "http://127.0.0.1:8080",
	})
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", logout.Code, logout.Body.String())
	}
	cookies := logout.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != session.Name || cookies[0].Path != session.Path || cookies[0].Secure != session.Secure || cookies[0].MaxAge != -1 {
		t.Fatalf("cleared cookie = %+v, session = %+v", cookies, session)
	}
	status = request(t, handler, http.MethodGet, SessionPath, "127.0.0.1:42000", map[string]string{"Cookie": session.String()})
	if status.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d", status.Code)
	}
}

func TestBrowserSessionTTLIsBounded(t *testing.T) {
	if _, err := New(nil, Config{Origin: "http://127.0.0.1:8080", SessionTTL: 12*time.Hour + time.Nanosecond}); err != store.ErrBrowserSessionInvalid {
		t.Fatalf("oversized session TTL error = %v", err)
	}
}

func TestBrowserEndpointsRejectNonLoopbackHostPeerAndQuery(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	password := "test-password-for-loopback-test"
	digest, err := localauth.HashPassword(rand.Reader, password, localauth.DefaultPasswordParameters())
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"
	if _, err := db.RegisterFirstOwner(context.Background(), authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity:         authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password:         digest,
		SessionToken:     sessionToken,
		Now:              now,
		SessionExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	handler := browserHandler(t, db, "http://127.0.0.1:8080", func() time.Time { return now })
	for _, test := range []struct {
		path   string
		remote string
		host   string
	}{
		{SessionPath, "192.0.2.4:42000", "127.0.0.1:8080"},
		{SessionPath, "127.0.0.1:42000", "localhost:8080"},
		{SessionPath + "?q=1", "127.0.0.1:42000", "127.0.0.1:8080"},
	} {
		recorder := requestWithHost(t, handler, http.MethodGet, test.path, test.remote, test.host, nil)
		if recorder.Code == http.StatusOK {
			t.Fatalf("unsafe request accepted: %+v", test)
		}
	}
}

func browserHandler(t *testing.T, db *store.Store, origin string, now func() time.Time) http.Handler {
	t.Helper()
	service, err := New(db, Config{Origin: origin, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := a.BrowserRequestMiddleware(origin, service)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func request(t *testing.T, handler http.Handler, method, path, remote string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithHost(t, handler, method, path, remote, "127.0.0.1:8080", headers)
}

func requestWithHost(t *testing.T, handler http.Handler, method, path, remote, host string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+host+path, nil)
	req.RemoteAddr = remote
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func createSessionFromLogin(t *testing.T, handler http.Handler, password string) *http.Cookie {
	t.Helper()
	login := localJSONRequest(t, handler, LocalLoginPath, "http://127.0.0.1:8080", map[string]any{
		"username": "owner", "password": password,
	}, nil)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %+v", cookies)
	}
	return cookies[0]
}
