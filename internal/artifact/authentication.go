package artifact

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
)

var browserSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type authenticator interface {
	AuthenticateHuman(context.Context, string) (store.Principal, error)
	AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (store.AgentRuntimeAuthentication, error)
	AuthenticateBrowserSession(context.Context, string, time.Time) (store.Principal, error)
}

type authenticationResolver struct {
	authenticator authenticator
	origin        string
}

func (r authenticationResolver) resolve(ctx context.Context, header http.Header, mutation bool, now time.Time) (store.ArtifactAuthentication, error) {
	request := http.Request{Header: header}
	cookies := request.CookiesNamed(authority.BrowserSessionCookieName)
	if len(header.Values("Authorization")) > 0 {
		if len(cookies) > 0 {
			return store.ArtifactAuthentication{}, unauthenticated()
		}
		credential, ok := authority.BearerCredential(header)
		if !ok {
			return store.ArtifactAuthentication{}, unauthenticated()
		}
		agent, agentErr := r.authenticator.AuthenticateAgentRuntimeSession(ctx, credential, now)
		if agentErr != nil && !errors.Is(agentErr, store.ErrAgentRuntimeUnauthenticated) {
			return store.ArtifactAuthentication{}, internalError()
		}
		human, humanErr := r.authenticator.AuthenticateHuman(ctx, credential)
		if humanErr != nil && !errors.Is(humanErr, store.ErrPermissionDenied) {
			return store.ArtifactAuthentication{}, internalError()
		}
		if agentErr == nil && humanErr == nil {
			return store.ArtifactAuthentication{}, unauthenticated()
		}
		if agentErr == nil {
			return store.ArtifactAuthentication{Agent: agent}, nil
		}
		if humanErr == nil {
			return store.ArtifactAuthentication{Human: human}, nil
		}
		return store.ArtifactAuthentication{}, unauthenticated()
	}
	if r.origin == "" || !authority.BrowserRequestAllowed(ctx) {
		return store.ArtifactAuthentication{}, unauthenticated()
	}
	if mutation {
		origins := header.Values("Origin")
		if len(origins) != 1 || origins[0] != r.origin {
			return store.ArtifactAuthentication{}, connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
		}
	}
	if len(cookies) != 1 || !browserSessionPattern.MatchString(cookies[0].Value) {
		return store.ArtifactAuthentication{}, unauthenticated()
	}
	human, err := r.authenticator.AuthenticateBrowserSession(ctx, cookies[0].Value, now)
	if err == nil {
		return store.ArtifactAuthentication{Human: human}, nil
	}
	if errors.Is(err, store.ErrPermissionDenied) {
		return store.ArtifactAuthentication{}, unauthenticated()
	}
	return store.ArtifactAuthentication{}, internalError()
}

func unauthenticated() error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("artifact authentication invalid"))
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("artifact service unavailable"))
}
