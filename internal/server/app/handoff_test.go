package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/endpoint"
)

func TestRequestBrowserHandoffReturnsTypedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+testHumanCredential {
			t.Fatal("missing Human authorization")
		}
		response.WriteHeader(http.StatusCreated)
		fmt.Fprintf(response, `{"path":"%s/%s","expires_at":"%s"}`, websession.CreateHandoffPath, testHandoffToken, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
	}))
	defer server.Close()
	serverEndpoint, err := endpoint.Parse(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := secureCredentialFile(t, testHumanCredential)
	handoff, err := RequestBrowserHandoff(context.Background(), serverEndpoint, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.URL.String() != server.URL+websession.CreateHandoffPath+"/"+testHandoffToken || handoff.ExpiresAt.IsZero() {
		t.Fatalf("handoff = %+v", handoff)
	}
}

func TestRequestBrowserHandoffErrorsStaySecretQuiet(t *testing.T) {
	serverEndpoint, err := endpoint.Parse("http://127.0.0.1:1", "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := secureCredentialFile(t, testHumanCredential)
	_, err = RequestBrowserHandoff(context.Background(), serverEndpoint, keyPath)
	if err == nil {
		t.Fatal("unavailable Server accepted")
	}
	if strings.Contains(err.Error(), keyPath) || strings.Contains(err.Error(), testHumanCredential) {
		t.Fatalf("secret leaked in error: %q", err)
	}
}
