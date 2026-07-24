package endpoint

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func NewHTTPClient(ep Endpoint) (*http.Client, error) {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is unavailable")
	}
	clone := t.Clone()
	if ep.Identity.Kind == IdentitySPKIPin {
		pin, err := base64.RawURLEncoding.DecodeString(ep.Identity.SPKIPin[len("sha256/"):])
		if err != nil || len(pin) != sha256.Size {
			return nil, errors.New("server SPKI pin is invalid")
		}
		clone.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         ep.url.Hostname(),
			InsecureSkipVerify: true,
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("server certificate is unavailable")
				}
				cert := state.PeerCertificates[0]
				now := time.Now()
				if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
					return errors.New("server certificate is expired or not yet valid")
				}
				digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
				if subtle.ConstantTimeCompare(digest[:], pin) != 1 {
					return errors.New("server SPKI pin does not match")
				}
				return nil
			},
		}
	}
	return &http.Client{
		Transport: rejectRedirectTransport{next: clone},
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

type rejectRedirectTransport struct {
	next http.RoundTripper
}

func (t rejectRedirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("server refused redirect status %d", resp.StatusCode)
	}
	return resp, nil
}
