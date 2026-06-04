package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/tool"
)

type barkTool struct {
	url    string
	client *http.Client
}

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterTool(&barkTool{
			url: strings.TrimSpace(a.BarkURL()),
			client: &http.Client{
				Timeout: 10 * time.Second,
			},
		})
		return nil
	}
}

func (t *barkTool) Name() string { return "notify_bark" }

func (t *barkTool) Risk() tool.RiskCategory { return tool.RiskNotification }

func (t *barkTool) Desc() string {
	return "Send a narrow Bark notification. Use this instead of curl/webhook commands for user notifications."
}

func (t *barkTool) Schema() map[string]any {
	return tool.ObjectSchema(
		tool.Prop("title", "string", "Short notification title"),
		tool.Prop("body", "string", "Notification body"),
		tool.Prop("level", "string", "Notification level: info, warning, critical"),
		tool.Prop("source", "string", "Short source label, used as notification group"),
		tool.Required("title", "body"),
	)
}

func (t *barkTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Level  string `json:"level"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("notify_bark: parse error: %w", err)
	}
	title := strings.TrimSpace(in.Title)
	body := strings.TrimSpace(in.Body)
	if title == "" || body == "" {
		return "", fmt.Errorf("notify_bark: title and body are required")
	}
	endpoint, err := barkEndpoint(t.url, title, body, in.Level, in.Source)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	client := t.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("notify_bark: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("notify_bark: http %d: %s", resp.StatusCode, strings.TrimSpace(string(text)))
	}
	return "notify_bark sent", nil
}

func barkEndpoint(base, title, body, level, source string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", fmt.Errorf("notify_bark: configure [notify].bark_url or SUMI_BARK_URL")
	}
	u, err := url.Parse(base + "/" + url.PathEscape(title) + "/" + url.PathEscape(body))
	if err != nil {
		return "", fmt.Errorf("notify_bark: invalid bark_url: %w", err)
	}
	q := u.Query()
	if v := normalizeLevel(level); v != "" {
		q.Set("level", v)
	}
	if src := strings.TrimSpace(source); src != "" {
		q.Set("group", src)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func normalizeLevel(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "info":
		return ""
	case "warning", "warn":
		return "timeSensitive"
	case "critical", "error":
		return "critical"
	default:
		return ""
	}
}
