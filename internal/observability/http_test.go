package observability

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPMiddlewareClassifiesAndRedactsAuthPath(t *testing.T) {
	var output bytes.Buffer
	logger := New(ComponentServer, &output)
	handler := HTTPMiddleware(logger, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "denied", http.StatusUnauthorized)
	}))
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/auth/private-token?secret=1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	logged := output.String()
	for _, want := range []string{"category=transport", "event=transport.http.completed", "method=POST", "path=/auth/:token", "status=401"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log output %q does not contain %q", logged, want)
		}
	}
	for _, forbidden := range []string{"private-token", "secret=1", "denied"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("log output leaked %q: %q", forbidden, logged)
		}
	}
}
