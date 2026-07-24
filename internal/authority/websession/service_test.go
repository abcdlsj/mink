package websession

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestBrowserHandoffHTTPIsSingleUseAndDoesNotClearExistingSession(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	credential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	if _, err := db.EnsureAuthority(context.Background(), credential, now); err != nil {
		t.Fatal(err)
	}
	handler := browserHandler(t, db, "http://127.0.0.1:8080", func() time.Time { return now })

	create := request(t, handler, http.MethodPost, CreateHandoffPath, "127.0.0.1:42000", map[string]string{"Authorization": "Bearer " + credential})
	if create.Code != http.StatusCreated || create.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create status/headers = %d %v", create.Code, create.Header())
	}
	var handoff handoffResponse
	if err := json.Unmarshal(create.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(handoff.Path, CreateHandoffPath+"/")
	if !opaqueTokenPattern.MatchString(token) || !handoff.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("handoff = %+v", handoff)
	}

	recorders := make([]*httptest.ResponseRecorder, 20)
	var wait sync.WaitGroup
	for index := range recorders {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			recorders[index] = request(t, handler, http.MethodGet, handoff.Path, "127.0.0.1:42000", nil)
		}(index)
	}
	wait.Wait()
	var session *http.Cookie
	for _, recorder := range recorders {
		if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/" || recorder.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("consume status/headers = %d %v", recorder.Code, recorder.Header())
		}
		cookies := recorder.Result().Cookies()
		if len(cookies) == 0 {
			continue
		}
		if session != nil || len(cookies) != 1 {
			t.Fatalf("multiple successful handoff consumptions")
		}
		session = cookies[0]
	}
	if session == nil {
		t.Fatal("handoff did not produce a session")
	}
	assertSessionCookie(t, session, now.Add(12*time.Hour), 12*60*60)

	status := request(t, handler, http.MethodGet, SessionPath, "127.0.0.1:42000", map[string]string{"Cookie": session.String()})
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"name":"Owner"`) {
		t.Fatalf("session status = %d %s", status.Code, status.Body.String())
	}
	status = request(t, handler, http.MethodGet, SessionPath, "127.0.0.1:42000", map[string]string{
		"Cookie": session.String(), "Authorization": "Bearer invalid-credential-abcdefghijklmnopqrstuvwxyz",
	})
	if status.Code != http.StatusUnauthorized {
		t.Fatalf("session status fell back from bearer to cookie: %d", status.Code)
	}
	replay := request(t, handler, http.MethodGet, handoff.Path, "127.0.0.1:42000", map[string]string{"Cookie": session.String()})
	if replay.Code != http.StatusSeeOther || len(replay.Result().Cookies()) != 0 {
		t.Fatalf("replay replaced existing session: %d %v", replay.Code, replay.Result().Cookies())
	}
	status = request(t, handler, http.MethodGet, SessionPath, "127.0.0.1:42000", map[string]string{"Cookie": session.String()})
	if status.Code != http.StatusOK {
		t.Fatalf("existing session after replay = %d", status.Code)
	}
}

func TestBrowserSessionLogoutRequiresExactOriginAndClearsCookie(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	credential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	if _, err := db.EnsureAuthority(context.Background(), credential, now); err != nil {
		t.Fatal(err)
	}
	handler := browserHandler(t, db, "http://127.0.0.1:8080", func() time.Time { return now })
	session := createSession(t, handler, credential)
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
	credential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	if _, err := db.EnsureAuthority(context.Background(), credential, now); err != nil {
		t.Fatal(err)
	}
	handler := browserHandler(t, db, "http://127.0.0.1:8080", func() time.Time { return now })
	for _, test := range []struct {
		path   string
		remote string
		host   string
	}{
		{CreateHandoffPath, "192.0.2.4:42000", "127.0.0.1:8080"},
		{CreateHandoffPath, "127.0.0.1:42000", "localhost:8080"},
		{CreateHandoffPath + "?actor_id=owner", "127.0.0.1:42000", "127.0.0.1:8080"},
	} {
		recorder := requestWithHost(t, handler, http.MethodPost, test.path, test.remote, test.host, map[string]string{"Authorization": "Bearer " + credential})
		if recorder.Code == http.StatusCreated {
			t.Fatalf("unsafe request created handoff: %+v", test)
		}
	}
	consume := requestWithHost(t, handler, http.MethodGet, CreateHandoffPath+"/abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP?x=1", "127.0.0.1:42000", "127.0.0.1:8080", nil)
	if consume.Code != http.StatusSeeOther || len(consume.Result().Cookies()) != 0 {
		t.Fatalf("query consume = %d %v", consume.Code, consume.Result().Cookies())
	}
	for _, framing := range []struct {
		contentLength    int64
		transferEncoding []string
	}{
		{-1, nil},
		{-1, []string{"chunked"}},
		{1, nil},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080"+CreateHandoffPath, nil)
		req.RemoteAddr = "127.0.0.1:42000"
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Body = unreadableBody{}
		req.ContentLength = framing.contentLength
		req.TransferEncoding = framing.transferEncoding
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("unsafe body framing %+v = %d", framing, recorder.Code)
		}
	}
}

type unreadableBody struct{}

func (unreadableBody) Read([]byte) (int, error) {
	panic("request body must not be read")
}

func (unreadableBody) Close() error {
	return nil
}

func browserHandler(t *testing.T, db *store.Store, origin string, now func() time.Time) http.Handler {
	t.Helper()
	service, err := New(db, Config{Origin: origin, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := authority.BrowserRequestMiddleware(origin, service)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func createSession(t *testing.T, handler http.Handler, credential string) *http.Cookie {
	t.Helper()
	create := request(t, handler, http.MethodPost, CreateHandoffPath, "127.0.0.1:42000", map[string]string{"Authorization": "Bearer " + credential})
	var handoff handoffResponse
	if err := json.Unmarshal(create.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	consume := request(t, handler, http.MethodGet, handoff.Path, "127.0.0.1:42000", nil)
	cookies := consume.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies = %+v", cookies)
	}
	return cookies[0]
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

func assertSessionCookie(t *testing.T, cookie *http.Cookie, expires time.Time, maxAge int) {
	t.Helper()
	if cookie.Name != authority.BrowserSessionCookieName || !cookie.HttpOnly || cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != maxAge || !cookie.Expires.Equal(expires) || !opaqueTokenPattern.MatchString(cookie.Value) {
		t.Fatalf("session cookie = %+v", cookie)
	}
}
