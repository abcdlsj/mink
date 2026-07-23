package workattention

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestListWorkAttentionItemsUsesBrowserHumanAndReturnsOnlyProjection(t *testing.T) {
	now := time.Date(2026, 7, 23, 18, 0, 0, 0, time.UTC)
	human := store.Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: uuid.NewString()}
	backend := &testStore{
		browser: human,
		items: []store.WorkAttentionItem{{
			WorkID: "work-id", SpaceID: "space-id", AgentID: "agent-id", Kind: "agent_exception", Status: "claimed", ReasonCode: "held_draft", UpdatedAt: now,
		}},
	}
	service := New(backend, "http://127.0.0.1:18080")
	service.now = func() time.Time { return now }
	request := connect.NewRequest(&inboxv1.ListWorkAttentionItemsRequest{Limit: 30})
	request.Header().Add("Cookie", authority.BrowserSessionCookieName+"="+strings.Repeat("b", 43))
	response, err := service.ListWorkAttentionItems(browserContext(t, "http://127.0.0.1:18080"), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.queries) != 1 || backend.queries[0].Human != human || backend.queries[0].Limit != 30 {
		t.Fatalf("store query = %+v", backend.queries)
	}
	if len(response.Msg.Items) != 1 {
		t.Fatalf("response = %+v", response.Msg)
	}
	item := response.Msg.Items[0]
	if item.WorkId != "work-id" || item.SpaceId != "space-id" || item.AgentId != "agent-id" || item.Kind != "agent_exception" || item.Status != "claimed" || item.ReasonCode != "held_draft" || item.UpdatedAt == nil || item.UpdatedAt.AsTime() != now {
		t.Fatalf("projection = %+v", item)
	}
}

func TestListWorkAttentionItemsRejectsNonBrowserAndKeepsBackendFailuresQuiet(t *testing.T) {
	now := time.Date(2026, 7, 23, 18, 0, 0, 0, time.UTC)
	backend := &testStore{browser: store.Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: uuid.NewString()}}
	service := New(backend, "http://127.0.0.1:18080")
	service.now = func() time.Time { return now }
	for _, name := range []string{"missing browser session", "bearer"} {
		t.Run(name, func(t *testing.T) {
			request := connect.NewRequest(&inboxv1.ListWorkAttentionItemsRequest{})
			if name == "bearer" {
				request.Header().Set("Authorization", "Bearer "+strings.Repeat("r", 43))
			}
			_, err := service.ListWorkAttentionItems(browserContext(t, "http://127.0.0.1:18080"), request)
			if connect.CodeOf(err) != connect.CodeUnauthenticated || len(backend.queries) != 0 {
				t.Fatalf("error/queries = %v/%+v", err, backend.queries)
			}
		})
	}
	backend.err = errors.New("sqlite private body and runtime basis")
	request := connect.NewRequest(&inboxv1.ListWorkAttentionItemsRequest{})
	request.Header().Add("Cookie", authority.BrowserSessionCookieName+"="+strings.Repeat("b", 43))
	_, err := service.ListWorkAttentionItems(browserContext(t, "http://127.0.0.1:18080"), request)
	if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "basis") {
		t.Fatalf("quiet backend error = %v", err)
	}
}

type testStore struct {
	browser store.Principal
	items   []store.WorkAttentionItem
	err     error
	queries []store.WorkAttentionQuery
}

func (s *testStore) AuthenticateHuman(context.Context, string) (authoritydomain.Principal, error) {
	return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
}

func (s *testStore) AuthenticateAgentRuntimeSession(context.Context, string, time.Time) (authorityapp.RuntimeAuthentication, error) {
	return authorityapp.RuntimeAuthentication{}, authorityapp.ErrRuntimeUnauthenticated
}

func (s *testStore) AuthenticateBrowserSession(context.Context, string, time.Time) (authoritydomain.Principal, error) {
	return s.browser, nil
}

func (s *testStore) ListWorkAttentionItems(_ context.Context, query store.WorkAttentionQuery) ([]store.WorkAttentionItem, error) {
	s.queries = append(s.queries, query)
	return s.items, s.err
}

func browserContext(t *testing.T, origin string) context.Context {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, origin, nil)
	request.RemoteAddr = "127.0.0.1:42000"
	var result context.Context
	handler, err := authority.BrowserRequestMiddleware(origin, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		result = request.Context()
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)
	return result
}
