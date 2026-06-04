package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBarkEndpoint(t *testing.T) {
	got, err := barkEndpoint("https://api.day.app/key/", "Bazaar 异常", "需要处理", "warning", "cron")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "https://api.day.app/key/Bazaar") {
		t.Fatalf("endpoint = %q", got)
	}
	if !strings.Contains(got, "level=timeSensitive") || !strings.Contains(got, "group=cron") {
		t.Fatalf("endpoint missing query: %q", got)
	}
}

func TestBarkToolSendsNarrowNotification(t *testing.T) {
	var path string
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		query = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tl := &barkTool{url: srv.URL + "/key", client: srv.Client()}
	out, err := tl.Run(context.Background(), json.RawMessage(`{"title":"Bazaar","body":"异常","level":"critical","source":"cron"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "notify_bark sent" {
		t.Fatalf("output = %q", out)
	}
	if path != "/key/Bazaar/%E5%BC%82%E5%B8%B8" {
		t.Fatalf("path = %q", path)
	}
	if !strings.Contains(query, "level=critical") || !strings.Contains(query, "group=cron") {
		t.Fatalf("query = %q", query)
	}
}

func TestBarkToolRequiresConfiguredURL(t *testing.T) {
	tl := &barkTool{}
	_, err := tl.Run(context.Background(), json.RawMessage(`{"title":"t","body":"b"}`))
	if err == nil || !strings.Contains(err.Error(), "SUMI_BARK_URL") {
		t.Fatalf("err = %v", err)
	}
}
