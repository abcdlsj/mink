package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxProviderResponseBytes = 16 << 20

type HTTPConfig struct {
	Endpoint string
	Model    string
	APIKey   string
	Client   *http.Client
}

func (config HTTPConfig) validate() error {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("provider endpoint must be an HTTPS URL without credentials, query, or fragment")
	}
	if strings.TrimSpace(config.Model) == "" || strings.TrimSpace(config.APIKey) == "" {
		return errors.New("provider model and credential are required")
	}
	return nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, reqBody, respValue any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("encode provider request: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create provider request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		hreq.Header.Set(name, value)
	}
	if client == nil {
		client = http.DefaultClient
	}
	hresp, err := client.Do(hreq)
	if err != nil {
		return fmt.Errorf("call provider: %w", err)
	}
	defer hresp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(hresp.Body, maxProviderResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read provider response: %w", err)
	}
	if len(body) > maxProviderResponseBytes {
		return errors.New("provider response exceeds size limit")
	}
	if hresp.StatusCode < 200 || hresp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d", hresp.StatusCode)
	}
	if err := json.Unmarshal(body, respValue); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

func endpointWithDefaultPath(raw, defaultPath string) string {
	parsed, _ := url.Parse(raw)
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = defaultPath
	}
	return parsed.String()
}
