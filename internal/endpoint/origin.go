package endpoint

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"strings"
)

type IdentityKind string

const (
	IdentityLiteralLoopback IdentityKind = "literal-loopback"
	IdentitySystemTrust     IdentityKind = "system-trust"
	IdentitySPKIPin         IdentityKind = "spki-pin"
)

type Identity struct {
	Kind    IdentityKind
	SPKIPin string
}

type Endpoint struct {
	Origin   string
	Identity Identity
	url      *url.URL
}

func Parse(rawOrigin, rawPin string) (Endpoint, error) {
	parsed, err := url.Parse(rawOrigin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return Endpoint{}, errors.New("server origin must contain only scheme and authority")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return Endpoint{}, errors.New("server origin scheme is invalid")
	}
	hostname := parsed.Hostname()
	if hostname == "" || strings.Contains(hostname, "%") {
		return Endpoint{}, errors.New("server origin host is invalid")
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	host := strings.ToLower(hostname)
	if ip := net.ParseIP(hostname); ip != nil {
		host = ip.String()
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	canonical := &url.URL{Scheme: scheme, Host: host}
	ip := net.ParseIP(hostname)
	literalLoopback := ip != nil && ip.IsLoopback()
	identity := Identity{}
	switch {
	case scheme == "http" && literalLoopback && rawPin == "":
		identity.Kind = IdentityLiteralLoopback
	case scheme == "http":
		return Endpoint{}, errors.New("HTTP server origin must use a literal loopback address")
	case rawPin == "":
		identity.Kind = IdentitySystemTrust
	default:
		if err := validatePin(rawPin); err != nil {
			return Endpoint{}, err
		}
		identity = Identity{Kind: IdentitySPKIPin, SPKIPin: rawPin}
	}
	return Endpoint{Origin: canonical.String(), Identity: identity, url: canonical}, nil
}

func FromIdentity(rawOrigin string, identity Identity) (Endpoint, error) {
	pin := ""
	if identity.Kind == IdentitySPKIPin {
		pin = identity.SPKIPin
	}
	parsed, err := Parse(rawOrigin, pin)
	if err != nil {
		return Endpoint{}, err
	}
	if parsed.Identity != identity {
		return Endpoint{}, errors.New("server identity does not match origin")
	}
	return parsed, nil
}

func validatePin(pin string) error {
	const prefix = "sha256/"
	if !strings.HasPrefix(pin, prefix) {
		return errors.New("server SPKI pin is invalid")
	}
	digest, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(pin, prefix))
	if err != nil || len(digest) != sha256.Size || prefix+base64.RawURLEncoding.EncodeToString(digest) != pin {
		return errors.New("server SPKI pin is invalid")
	}
	return nil
}
