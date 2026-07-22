package authentication

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

var (
	ErrUnauthenticated = errors.New("authentication invalid")
	ErrUnavailable     = errors.New("authentication unavailable")
	ErrSameOrigin      = errors.New("same-origin browser request required")
)

var browserSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type Authenticator interface {
	AuthenticateHuman(context.Context, string) (authoritydomain.Principal, error)
	AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (authorityapp.RuntimeAuthentication, error)
	AuthenticateBrowserSession(context.Context, string, time.Time) (authoritydomain.Principal, error)
}

type resultKind uint8

const (
	resultKindInvalid resultKind = iota
	resultKindHuman
	resultKindAgent
)

type Result interface {
	Human() (authoritydomain.Principal, bool)
	Agent() (authorityapp.RuntimeAuthentication, bool)
}

type result struct {
	kind  resultKind
	human authoritydomain.Principal
	agent authorityapp.RuntimeAuthentication
}

func humanResult(human authoritydomain.Principal) Result {
	return result{kind: resultKindHuman, human: human}
}

func agentResult(agent authorityapp.RuntimeAuthentication) Result {
	return result{kind: resultKindAgent, agent: agent}
}

func (r result) Human() (authoritydomain.Principal, bool) {
	return r.human, r.kind == resultKindHuman
}

func (r result) Agent() (authorityapp.RuntimeAuthentication, bool) {
	return r.agent, r.kind == resultKindAgent
}

func Resolve(ctx context.Context, authenticator Authenticator, header http.Header, mutation bool, origin string, now time.Time) (Result, error) {
	request := http.Request{Header: header}
	cookies := request.CookiesNamed(authority.BrowserSessionCookieName)
	if len(header.Values("Authorization")) > 0 {
		if len(cookies) > 0 {
			return nil, ErrUnauthenticated
		}
		credential, ok := authority.BearerCredential(header)
		if !ok {
			return nil, ErrUnauthenticated
		}
		agent, agentErr := authenticator.AuthenticateAgentRuntimeSession(ctx, credential, now)
		if agentErr != nil && !errors.Is(agentErr, authorityapp.ErrRuntimeUnauthenticated) {
			return nil, ErrUnavailable
		}
		human, humanErr := authenticator.AuthenticateHuman(ctx, credential)
		if humanErr != nil && !errors.Is(humanErr, authoritydomain.ErrPermissionDenied) {
			return nil, ErrUnavailable
		}
		if agentErr == nil && humanErr == nil {
			return nil, ErrUnauthenticated
		}
		if agentErr == nil {
			return agentResult(agent), nil
		}
		if humanErr == nil {
			return humanResult(human), nil
		}
		return nil, ErrUnauthenticated
	}
	if origin == "" || !authority.BrowserRequestAllowed(ctx) {
		return nil, ErrUnauthenticated
	}
	if mutation {
		origins := header.Values("Origin")
		if len(origins) != 1 || origins[0] != origin {
			return nil, ErrSameOrigin
		}
	}
	if len(cookies) != 1 || !browserSessionPattern.MatchString(cookies[0].Value) {
		return nil, ErrUnauthenticated
	}
	human, err := authenticator.AuthenticateBrowserSession(ctx, cookies[0].Value, now)
	if err == nil {
		return humanResult(human), nil
	}
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		return nil, ErrUnauthenticated
	}
	return nil, ErrUnavailable
}
