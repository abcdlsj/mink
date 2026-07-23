package app

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
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/endpoint"
)

type BrowserHandoff struct {
	URL       *url.URL
	ExpiresAt time.Time
}

func RequestBrowserHandoff(ctx context.Context, serverEndpoint endpoint.Endpoint, humanKeyFile string) (BrowserHandoff, error) {
	validated, err := endpoint.FromIdentity(serverEndpoint.Origin, serverEndpoint.Identity)
	if err != nil || validated.Origin != serverEndpoint.Origin {
		return BrowserHandoff{}, errors.New("auth Server endpoint is unsafe")
	}
	origin, err := url.Parse(validated.Origin)
	if err != nil {
		return BrowserHandoff{}, errors.New("auth Server endpoint is unsafe")
	}
	credential, err := authority.ReadCredentialFile(humanKeyFile)
	if err != nil {
		return BrowserHandoff{}, errors.New("human credential file is missing or unsafe")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, validated.Origin+websession.CreateHandoffPath, nil)
	if err != nil {
		return BrowserHandoff{}, errors.New("create browser authentication request")
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	client, err := endpoint.NewHTTPClient(validated)
	if err != nil {
		return BrowserHandoff{}, errors.New("create browser authentication transport")
	}
	response, err := client.Do(request)
	if err != nil {
		return BrowserHandoff{}, errors.New("browser authentication request failed")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return BrowserHandoff{}, errors.New("browser authentication request refused a redirect")
	}
	if response.StatusCode != http.StatusCreated {
		return BrowserHandoff{}, fmt.Errorf("browser authentication request returned status %d", response.StatusCode)
	}
	var payloadBody struct {
		Path      string    `json:"path"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2049))
	if err != nil || len(payload) > 2048 {
		return BrowserHandoff{}, errors.New("browser authentication response is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payloadBody); err != nil {
		return BrowserHandoff{}, errors.New("browser authentication response is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return BrowserHandoff{}, errors.New("browser authentication response is invalid")
	}
	if payloadBody.ExpiresAt.IsZero() {
		return BrowserHandoff{}, errors.New("browser authentication response is invalid")
	}
	handoffURL, err := resolveHandoffURL(origin, payloadBody.Path)
	if err != nil {
		return BrowserHandoff{}, errors.New("browser authentication response is unsafe")
	}
	return BrowserHandoff{URL: handoffURL, ExpiresAt: payloadBody.ExpiresAt}, nil
}

func resolveHandoffURL(origin *url.URL, path string) (*url.URL, error) {
	handoff, err := url.Parse(path)
	if err != nil || handoff.IsAbs() || handoff.Host != "" || handoff.RawQuery != "" || handoff.Fragment != "" || !strings.HasPrefix(handoff.Path, websession.CreateHandoffPath+"/") {
		return nil, errors.New("invalid handoff path")
	}
	token := strings.TrimPrefix(handoff.Path, websession.CreateHandoffPath+"/")
	if len(token) != 43 || strings.Contains(token, "/") || handoff.EscapedPath() != handoff.Path {
		return nil, errors.New("invalid handoff token")
	}
	for _, character := range token {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return nil, errors.New("invalid handoff token")
		}
	}
	resolved := origin.ResolveReference(handoff)
	if resolved.Scheme != origin.Scheme || resolved.Host != origin.Host {
		return nil, errors.New("cross-origin handoff")
	}
	return resolved, nil
}
