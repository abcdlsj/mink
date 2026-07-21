package artifact

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestAuthenticationResolverRejectsAmbiguousIdentities(t *testing.T) {
	token := strings.Repeat("a", 43)
	human := store.Principal{Kind: "human", ID: "human-id", OrganizationID: "organization-id"}
	agent := store.AgentRuntimeAuthentication{
		Principal: store.Principal{Kind: "agent", ID: "agent-id", OrganizationID: "organization-id"},
	}
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)

	t.Run("bearer matching human and runtime", func(t *testing.T) {
		resolver := authenticationResolver{authenticator: &fakeAuthenticator{human: human, agent: agent}}
		header := http.Header{"Authorization": {"Bearer " + token}}
		_, err := resolver.resolve(context.Background(), header, false, now)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("ambiguous bearer error = %v", err)
		}
	})

	t.Run("bearer and browser cookie", func(t *testing.T) {
		resolver := authenticationResolver{authenticator: &fakeAuthenticator{human: human}, origin: "http://127.0.0.1:18080"}
		header := http.Header{"Authorization": {"Bearer " + token}}
		header.Add("Cookie", authority.BrowserSessionCookieName+"="+token)
		_, err := resolver.resolve(context.Background(), header, true, now)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("multiple identity sources error = %v", err)
		}
	})

	t.Run("one bearer identity", func(t *testing.T) {
		header := http.Header{"Authorization": {"Bearer " + token}}
		resolved, err := (authenticationResolver{authenticator: &fakeAuthenticator{human: human}}).resolve(context.Background(), header, false, now)
		if err != nil || resolved.Human != human || resolved.Agent.Principal.ID != "" {
			t.Fatalf("human bearer = %+v, %v", resolved, err)
		}
		resolved, err = (authenticationResolver{authenticator: &fakeAuthenticator{agent: agent}}).resolve(context.Background(), header, false, now)
		if err != nil || resolved.Human.ID != "" || resolved.Agent.Principal != agent.Principal {
			t.Fatalf("agent bearer = %+v, %v", resolved, err)
		}
	})
}

func TestServiceErrorDoesNotExposeBackendDetails(t *testing.T) {
	backend := fmt.Errorf("sqlite /private/path leaked-secret")
	err := serviceError(backend)
	if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("quiet service error = %v", err)
	}
}

type fakeAuthenticator struct {
	human   store.Principal
	agent   store.AgentRuntimeAuthentication
	browser store.Principal
}

func (f *fakeAuthenticator) AuthenticateHuman(context.Context, string) (store.Principal, error) {
	if f.human.ID == "" {
		return store.Principal{}, store.ErrPermissionDenied
	}
	return f.human, nil
}

func (f *fakeAuthenticator) AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (store.AgentRuntimeAuthentication, error) {
	if f.agent.Principal.ID == "" {
		return store.AgentRuntimeAuthentication{}, store.ErrAgentRuntimeUnauthenticated
	}
	return f.agent, nil
}

func (f *fakeAuthenticator) AuthenticateBrowserSession(context.Context, string, time.Time) (store.Principal, error) {
	if f.browser.ID == "" {
		return store.Principal{}, store.ErrPermissionDenied
	}
	return f.browser, nil
}

var _ authenticator = (*fakeAuthenticator)(nil)
