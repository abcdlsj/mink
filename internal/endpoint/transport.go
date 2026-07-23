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

func NewHTTPClient(endpoint Endpoint) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is unavailable")
	}
	clone := transport.Clone()
	if endpoint.Identity.Kind == IdentitySPKIPin {
		pin, err := base64.RawURLEncoding.DecodeString(endpoint.Identity.SPKIPin[len("sha256/"):])
		if err != nil || len(pin) != sha256.Size {
			return nil, errors.New("server SPKI pin is invalid")
		}
		clone.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         endpoint.url.Hostname(),
			InsecureSkipVerify: true,
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("server certificate is unavailable")
				}
				certificate := state.PeerCertificates[0]
				now := time.Now()
				if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
					return errors.New("server certificate is expired or not yet valid")
				}
				digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
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

func (transport rejectRedirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		response.Body.Close()
		return nil, fmt.Errorf("server refused redirect status %d", response.StatusCode)
	}
	return response, nil
}
