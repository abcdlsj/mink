package artifact

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	artifactapp "github.com/abcdlsj/sumi/internal/artifact/application"
	sharedauthentication "github.com/abcdlsj/sumi/internal/authentication"
)

type authenticator = sharedauthentication.Authenticator

type authenticationResolver struct {
	authenticator authenticator
	origin        string
}

func (r authenticationResolver) resolve(ctx context.Context, header http.Header, mutation bool, now time.Time) (artifactapp.Authentication, error) {
	result, err := sharedauthentication.Resolve(ctx, r.authenticator, header, mutation, r.origin, now)
	if errors.Is(err, sharedauthentication.ErrUnauthenticated) {
		return artifactapp.Authentication{}, unauthenticated()
	}
	if errors.Is(err, sharedauthentication.ErrSameOrigin) {
		return artifactapp.Authentication{}, connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
	}
	if err != nil {
		return artifactapp.Authentication{}, internalError()
	}
	if human, ok := result.Human(); ok {
		return artifactapp.Authentication{Human: human}, nil
	}
	if agent, ok := result.Agent(); ok {
		return artifactapp.Authentication{Agent: agent}, nil
	}
	return artifactapp.Authentication{}, internalError()
}

func unauthenticated() error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("artifact authentication invalid"))
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("artifact service unavailable"))
}
