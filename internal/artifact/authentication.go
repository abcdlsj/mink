package artifact

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"connectrpc.com/connect"
	artifactapp "github.com/abcdlsj/sumi/internal/artifact/application"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

var browserSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type authenticator interface {
	AuthenticateHuman(context.Context, string) (authoritydomain.Principal, error)
	AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (authorityapp.RuntimeAuthentication, error)
	AuthenticateBrowserSession(context.Context, string, time.Time) (authoritydomain.Principal, error)
}

type authenticationResolver struct {
	authenticator authenticator
	origin        string
}

func (r authenticationResolver) resolve(ctx context.Context, header http.Header, mutation bool, now time.Time) (artifactapp.Authentication, error) {
	request := http.Request{Header: header}
	cookies := request.CookiesNamed(authority.BrowserSessionCookieName)
	if len(header.Values("Authorization")) > 0 {
		if len(cookies) > 0 {
			return artifactapp.Authentication{}, unauthenticated()
		}
		credential, ok := authority.BearerCredential(header)
		if !ok {
			return artifactapp.Authentication{}, unauthenticated()
		}
		agent, agentErr := r.authenticator.AuthenticateAgentRuntimeSession(ctx, credential, now)
		if agentErr != nil && !errors.Is(agentErr, authorityapp.ErrRuntimeUnauthenticated) {
			return artifactapp.Authentication{}, internalError()
		}
		human, humanErr := r.authenticator.AuthenticateHuman(ctx, credential)
		if humanErr != nil && !errors.Is(humanErr, authoritydomain.ErrPermissionDenied) {
			return artifactapp.Authentication{}, internalError()
		}
		if agentErr == nil && humanErr == nil {
			return artifactapp.Authentication{}, unauthenticated()
		}
		if agentErr == nil {
			return artifactapp.Authentication{Agent: agent}, nil
		}
		if humanErr == nil {
			return artifactapp.Authentication{Human: human}, nil
		}
		return artifactapp.Authentication{}, unauthenticated()
	}
	if r.origin == "" || !authority.BrowserRequestAllowed(ctx) {
		return artifactapp.Authentication{}, unauthenticated()
	}
	if mutation {
		origins := header.Values("Origin")
		if len(origins) != 1 || origins[0] != r.origin {
			return artifactapp.Authentication{}, connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
		}
	}
	if len(cookies) != 1 || !browserSessionPattern.MatchString(cookies[0].Value) {
		return artifactapp.Authentication{}, unauthenticated()
	}
	human, err := r.authenticator.AuthenticateBrowserSession(ctx, cookies[0].Value, now)
	if err == nil {
		return artifactapp.Authentication{Human: human}, nil
	}
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		return artifactapp.Authentication{}, unauthenticated()
	}
	return artifactapp.Authentication{}, internalError()
}

func unauthenticated() error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("artifact authentication invalid"))
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("artifact service unavailable"))
}
