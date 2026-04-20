package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/tool"
)

const braveEndpoint = "https://api.search.brave.com/res/v1/web/search"

type brave struct {
	key    string
	client *http.Client
}

func Plugin() app.Plugin {
	return func(a *app.App) error {
		key := strings.TrimSpace(a.Config().BraveSearch.APIKey)
		if key == "" {
			return nil
		}
		a.RegisterTool(&brave{
			key:    key,
			client: &http.Client{Timeout: 20 * time.Second},
		})
		return nil
	}
}

func (b *brave) Name() string { return "brave_search" }

func (b *brave) Desc() string {
	return "Search the web using Brave Search API"
}

func (b *brave) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":  map[string]any{"type": "string", "description": "Search query"},
			"count":  map[string]any{"type": "integer", "description": "Result count, default 5"},
			"offset": map[string]any{"type": "integer", "description": "Result offset"},
		},
		"required": []string{"query"},
	}
}

func (b *brave) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Query  string `json:"query"`
		Count  int    `json:"count"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", tool.ParseError(b.Name(), err.Error(), string(args))
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	count := in.Count
	if count <= 0 {
		count = 5
	}
	if count > 20 {
		count = 20
	}
	q := url.Values{}
	q.Set("q", strings.TrimSpace(in.Query))
	q.Set("count", strconv.Itoa(count))
	if in.Offset > 0 {
		q.Set("offset", strconv.Itoa(in.Offset))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveEndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return "", tool.WrapError(b.Name(), err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.key)
	resp, err := b.client.Do(req)
	if err != nil {
		return "", tool.WrapError(b.Name(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", tool.WrapError(b.Name(), err)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("brave search request failed: %s", msg)
	}
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return string(body), nil
	}
	if len(out.Web.Results) == 0 {
		return "(no results)", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Brave results for %q:\n", strings.TrimSpace(in.Query))
	for i, r := range out.Web.Results {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, blank(r.Title, "(untitled)"))
		if strings.TrimSpace(r.URL) != "" {
			fmt.Fprintf(&sb, "   %s\n", strings.TrimSpace(r.URL))
		}
		if strings.TrimSpace(r.Description) != "" {
			fmt.Fprintf(&sb, "   %s\n", strings.TrimSpace(r.Description))
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}
