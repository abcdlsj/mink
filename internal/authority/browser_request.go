package authority

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
)

var ErrBrowserOriginInvalid = errors.New("browser origin must be a loopback HTTP or HTTPS origin")

type browserRequestKey struct{}

func BrowserRequestMiddleware(origin string, next http.Handler) (http.Handler, error) {
	u, err := parseBrowserOrigin(origin)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := u != nil && requestMatchesBrowserOrigin(r, u)
		ctx := context.WithValue(r.Context(), browserRequestKey{}, allowed)
		next.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

func BrowserRequestAllowed(ctx context.Context) bool {
	allowed, _ := ctx.Value(browserRequestKey{}).(bool)
	return allowed
}

func BrowserOriginSecure(origin string) (bool, error) {
	parsed, err := parseBrowserOrigin(origin)
	if err != nil {
		return false, err
	}
	return parsed != nil && parsed.Scheme == "https", nil
}

func ValidateBrowserOrigin(origin string) error {
	_, err := parseBrowserOrigin(origin)
	return err
}

func parseBrowserOrigin(origin string) (*url.URL, error) {
	if origin == "" {
		return nil, nil
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return nil, ErrBrowserOriginInvalid
	}
	hostname := parsed.Hostname()
	if hostname != "localhost" {
		ip := net.ParseIP(hostname)
		if ip == nil || !ip.IsLoopback() {
			return nil, ErrBrowserOriginInvalid
		}
	}
	return parsed, nil
}

func requestMatchesBrowserOrigin(r *http.Request, origin *url.URL) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || r.Host != origin.Host {
		return false
	}
	return (r.TLS != nil) == (origin.Scheme == "https")
}
