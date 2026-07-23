package websession

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestLocalSetupAndLoginUseTheExistingHumanSession(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "server.db")
	database, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	bootstrapCredential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	password := "correct horse battery staple"
	if _, err := database.EnsureAuthority(context.Background(), bootstrapCredential, now); err != nil {
		t.Fatal(err)
	}
	handler := localBrowserHandler(t, database, "http://127.0.0.1:8080", func() time.Time { return now })

	status := request(t, handler, http.MethodGet, LocalStatusPath, "127.0.0.1:42000", nil)
	if status.Code != http.StatusOK || status.Header().Get("Cache-Control") != "no-store" || status.Body.String() != "{\"setup_required\":true}\n" {
		t.Fatalf("initial local status = %d %v %q", status.Code, status.Header(), status.Body.String())
	}
	badSetup := localJSONRequest(t, handler, LocalSetupPath, "http://127.0.0.1:8080", map[string]any{
		"username": "Owner", "password": password, "bootstrap_credential": strings.Repeat("A", 43),
	}, nil)
	if badSetup.Code != http.StatusUnauthorized || badSetup.Body.Len() != 0 || len(badSetup.Result().Cookies()) != 0 {
		t.Fatalf("bad setup = %d %q %v", badSetup.Code, badSetup.Body.String(), badSetup.Result().Cookies())
	}
	setup := localJSONRequest(t, handler, LocalSetupPath, "http://127.0.0.1:8080", map[string]any{
		"username": "Owner", "password": password, "bootstrap_credential": bootstrapCredential,
	}, nil)
	if setup.Code != http.StatusCreated || !strings.Contains(setup.Body.String(), `"name":"Owner"`) {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	cookies := setup.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("setup cookies = %+v", cookies)
	}
	assertSessionCookie(t, cookies[0], now.Add(12*time.Hour), 12*60*60)
	status = request(t, handler, http.MethodGet, SessionPath, "127.0.0.1:42000", map[string]string{"Cookie": cookies[0].String()})
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"name":"Owner"`) {
		t.Fatalf("setup session = %d %s", status.Code, status.Body.String())
	}
	status = request(t, handler, http.MethodGet, LocalStatusPath, "127.0.0.1:42000", nil)
	if status.Code != http.StatusOK || status.Body.String() != "{\"setup_required\":false}\n" {
		t.Fatalf("completed local status = %d %q", status.Code, status.Body.String())
	}
	duplicate := localJSONRequest(t, handler, LocalSetupPath, "http://127.0.0.1:8080", map[string]any{
		"username": "second", "password": password, "bootstrap_credential": bootstrapCredential,
	}, nil)
	if duplicate.Code != http.StatusConflict || duplicate.Body.Len() != 0 {
		t.Fatalf("duplicate setup = %d %q", duplicate.Code, duplicate.Body.String())
	}

	logout := request(t, handler, http.MethodPost, LogoutPath, "127.0.0.1:42000", map[string]string{
		"Cookie": cookies[0].String(), "Origin": "http://127.0.0.1:8080",
	})
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", logout.Code)
	}
	for _, credentials := range []map[string]any{
		{"username": "missing", "password": password},
		{"username": "Owner", "password": "this password is incorrect"},
	} {
		failed := localJSONRequest(t, handler, LocalLoginPath, "http://127.0.0.1:8080", credentials, nil)
		if failed.Code != http.StatusUnauthorized || failed.Body.Len() != 0 || len(failed.Result().Cookies()) != 0 {
			t.Fatalf("failed login = %d %q %v", failed.Code, failed.Body.String(), failed.Result().Cookies())
		}
	}
	login := localJSONRequest(t, handler, LocalLoginPath, "http://127.0.0.1:8080", map[string]any{
		"username": "OWNER", "password": password,
	}, nil)
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `"name":"Owner"`) || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login = %d %s %v", login.Code, login.Body.String(), login.Result().Cookies())
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		payload, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		if bytes.Contains(payload, []byte(password)) || bytes.Contains(payload, []byte(bootstrapCredential)) {
			t.Fatalf("raw local password or bootstrap credential leaked into %s", filepath.Base(path))
		}
	}
}

func TestLocalAuthMutationsRequireExactOriginAndBoundedJSON(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	credential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	if _, err := database.EnsureAuthority(context.Background(), credential, now); err != nil {
		t.Fatal(err)
	}
	handler := localBrowserHandler(t, database, "http://127.0.0.1:8080", func() time.Time { return now })
	payload := map[string]any{"username": "owner", "password": "correct horse battery staple", "bootstrap_credential": credential}
	for _, origin := range []string{"", "http://localhost:8080", "http://127.0.0.1:8080/"} {
		response := localJSONRequest(t, handler, LocalSetupPath, origin, payload, nil)
		if response.Code != http.StatusForbidden {
			t.Fatalf("origin %q setup = %d", origin, response.Code)
		}
	}
	response := localJSONRequest(t, handler, LocalSetupPath, "http://127.0.0.1:8080", payload, map[string]string{"Authorization": "Bearer " + credential})
	if response.Code != http.StatusForbidden {
		t.Fatalf("mixed auth carrier setup = %d", response.Code)
	}
	response = localJSONRequest(t, handler, LocalSetupPath, "http://127.0.0.1:8080", map[string]any{
		"username": "owner", "password": "correct horse battery staple", "bootstrap_credential": credential, "unexpected": true,
	}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field setup = %d", response.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080"+LocalSetupPath, strings.NewReader(strings.Repeat("x", localAuthBodyLimit+1)))
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("Content-Type", "application/json")
	oversized := httptest.NewRecorder()
	handler.ServeHTTP(oversized, request)
	if oversized.Code != http.StatusBadRequest {
		t.Fatalf("oversized JSON setup = %d", oversized.Code)
	}
}

func TestLocalLoginRateLimitIsBoundedAndRecovers(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	credential := "bootstrap-credential-abcdefghijklmnopqrstuvwxyz-0123456789"
	if _, err := database.EnsureAuthority(context.Background(), credential, now); err != nil {
		t.Fatal(err)
	}
	handler := localBrowserHandler(t, database, "http://127.0.0.1:8080", func() time.Time { return now })
	credentials := map[string]any{"username": "missing", "password": "incorrect password value"}
	for attempt := 0; attempt < loginFailureLimit; attempt++ {
		response := localJSONRequest(t, handler, LocalLoginPath, "http://127.0.0.1:8080", credentials, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", attempt+1, response.Code)
		}
	}
	blocked := localJSONRequest(t, handler, LocalLoginPath, "http://127.0.0.1:8080", credentials, nil)
	if blocked.Code != http.StatusTooManyRequests || blocked.Body.Len() != 0 {
		t.Fatalf("blocked login = %d %q", blocked.Code, blocked.Body.String())
	}
	now = now.Add(loginBlockDuration + time.Second)
	recovered := localJSONRequest(t, handler, LocalLoginPath, "http://127.0.0.1:8080", credentials, nil)
	if recovered.Code != http.StatusUnauthorized {
		t.Fatalf("recovered login = %d", recovered.Code)
	}
}

func TestPasswordDerivationConcurrencyIsBounded(t *testing.T) {
	service := &Service{passwordSlots: make(chan struct{}, 2)}
	for slot := 0; slot < 2; slot++ {
		if !service.acquirePasswordSlot() {
			t.Fatalf("password derivation slot %d rejected available capacity", slot+1)
		}
	}
	if service.acquirePasswordSlot() {
		t.Fatal("password derivation slots exceeded bounded capacity")
	}
	service.releasePasswordSlot()
	if !service.acquirePasswordSlot() {
		t.Fatal("password derivation slot did not recover after release")
	}
	service.releasePasswordSlot()
	service.releasePasswordSlot()
}

func localBrowserHandler(t *testing.T, database *store.Store, origin string, now func() time.Time) http.Handler {
	t.Helper()
	service, err := New(database, Config{
		Origin: origin, Now: now,
		passwordParameters: passwordParameters{memory: 8192, iterations: 1, parallelism: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := authority.BrowserRequestMiddleware(origin, service)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func localJSONRequest(t *testing.T, handler http.Handler, path, origin string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080"+path, bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
