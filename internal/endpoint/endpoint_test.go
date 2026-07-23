package endpoint

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginAndIdentityMatrix(t *testing.T) {
	validPin := "sha256/" + base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))
	for _, test := range []struct {
		origin string
		pin    string
		want   string
		kind   IdentityKind
	}{
		{"http://127.0.0.1:80", "", "http://127.0.0.1", IdentityLiteralLoopback},
		{"http://[::1]:8080", "", "http://[::1]:8080", IdentityLiteralLoopback},
		{"https://EXAMPLE.com:443", "", "https://example.com", IdentitySystemTrust},
		{"https://example.com", validPin, "https://example.com", IdentitySPKIPin},
	} {
		endpoint, err := Parse(test.origin, test.pin)
		if err != nil || endpoint.Origin != test.want || endpoint.Identity.Kind != test.kind {
			t.Fatalf("Parse(%q) = %+v, %v", test.origin, endpoint, err)
		}
		if _, err := FromIdentity(endpoint.Origin, endpoint.Identity); err != nil {
			t.Fatalf("FromIdentity() = %v", err)
		}
	}
	for _, origin := range []string{"http://localhost:8080", "http://192.0.2.1", "https://example.com/", "https://user@example.com", "ftp://127.0.0.1"} {
		if _, err := Parse(origin, ""); err == nil {
			t.Fatalf("unsafe origin %q was accepted", origin)
		}
	}
	if _, err := Parse("https://example.com", "sha256/not-a-pin"); err == nil {
		t.Fatal("invalid SPKI pin was accepted")
	}
}

func TestSystemTrustPinAndRedirectTransport(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer tlsServer.Close()
	systemEndpoint, err := Parse(tlsServer.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	systemClient, err := NewHTTPClient(systemEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := systemClient.Get(tlsServer.URL); err == nil {
		t.Fatal("self-signed certificate passed system trust")
	}
	certificate := tlsServer.Certificate()
	digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	pin := "sha256/" + base64.RawURLEncoding.EncodeToString(digest[:])
	pinnedEndpoint, err := Parse(tlsServer.URL, pin)
	if err != nil {
		t.Fatal(err)
	}
	pinnedClient, err := NewHTTPClient(pinnedEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	response, err := pinnedClient.Get(tlsServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	var targetRequests int
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetRequests++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	redirectEndpoint, err := Parse(redirect.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewHTTPClient(redirectEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, redirect.URL, http.NoBody)
	request.Header.Set("Authorization", "Bearer quiet-secret")
	if _, err := client.Do(request); err == nil {
		t.Fatal("redirect was accepted")
	}
	if targetRequests != 0 {
		t.Fatalf("redirect forwarded bearer to target %d times", targetRequests)
	}
}
