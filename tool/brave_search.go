package tool

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
)

const braveSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"

type BraveSearch struct {
	apiKey string
	client *http.Client
}

func NewBraveSearch(apiKey string) *BraveSearch {
	return &BraveSearch{
		apiKey: strings.TrimSpace(apiKey),
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (b *BraveSearch) Name() string { return "brave_search" }
func (b *BraveSearch) Desc() string { return "Search the web using Brave Search API" }

func (b *BraveSearch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Number of results to return (1-20, default 5)",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Result offset (optional)",
			},
		},
		"required": []string{"query"},
	}
}

func (b *BraveSearch) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if b.apiKey == "" {
		return "", fmt.Errorf("brave search api key is not configured")
	}

	var params struct {
		Query  string `json:"query"`
		Count  int    `json:"count,omitempty"`
		Offset int    `json:"offset,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", ParseError("brave_search", err.Error())
	}

	query := strings.TrimSpace(params.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	count := params.Count
	if count <= 0 {
		count = 5
	}
	if count > 20 {
		count = 20
	}

	v := url.Values{}
	v.Set("q", query)
	v.Set("count", strconv.Itoa(count))
	if params.Offset > 0 {
		v.Set("offset", strconv.Itoa(params.Offset))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveSearchEndpoint+"?"+v.Encode(), nil)
	if err != nil {
		return "", WrapError("brave_search", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.apiKey)

	resp, err := b.client.Do(req)
	if err != nil {
		return "", WrapError("brave_search", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", WrapError("brave_search", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("brave search request failed: %s", truncate(msg, 500))
	}

	var payload struct {
		Web struct {
			Results []struct {
				Title         string   `json:"title"`
				URL           string   `json:"url"`
				Description   string   `json:"description"`
				ExtraSnippets []string `json:"extra_snippets"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return string(body), nil
	}

	if len(payload.Web.Results) == 0 {
		return "(no results)", nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Brave results for %q:\n", query)
	for i, r := range payload.Web.Results {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = "(untitled)"
		}
		link := strings.TrimSpace(r.URL)
		desc := strings.TrimSpace(r.Description)

		fmt.Fprintf(&out, "%d. %s\n", i+1, title)
		if link != "" {
			fmt.Fprintf(&out, "   %s\n", link)
		}
		if desc != "" {
			fmt.Fprintf(&out, "   %s\n", desc)
		}
		if len(r.ExtraSnippets) > 0 {
			fmt.Fprintf(&out, "   %s\n", strings.TrimSpace(r.ExtraSnippets[0]))
		}
	}

	return strings.TrimSpace(out.String()), nil
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
