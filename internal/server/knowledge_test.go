package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	knowledgev1 "github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1/knowledgev1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/store"
)

func TestKnowledgeTransportUsesSharedAuthenticationResolver(t *testing.T) {
	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	humanToken := strings.Repeat("h", 43)
	agentToken := strings.Repeat("a", 43)
	ambiguousToken := strings.Repeat("x", 43)
	human := authoritydomain.Principal{Kind: authoritydomain.PrincipalHuman, ID: "human-id", OrganizationID: "organization-id"}
	agent := authorityapp.RuntimeAuthentication{Principal: authoritydomain.Principal{Kind: authoritydomain.PrincipalAgent, ID: "agent-id", OrganizationID: "organization-id"}}
	backend := &testKnowledgeBackend{
		humans: map[string]authoritydomain.Principal{humanToken: human, ambiguousToken: human},
		agents: map[string]authorityapp.RuntimeAuthentication{agentToken: agent, ambiguousToken: agent},
		page:   store.KnowledgeSearchOutput{Status: store.KnowledgeIndexReady},
	}
	service := newKnowledgeService(backend, "http://127.0.0.1:18080")
	service.now = func() time.Time { return now }

	for name, test := range map[string]struct {
		token string
		check func(store.KnowledgeSearchParams) bool
	}{
		"human bearer":  {token: humanToken, check: func(params store.KnowledgeSearchParams) bool { return params.Human == human && !params.Agent.Valid() }},
		"agent runtime": {token: agentToken, check: func(params store.KnowledgeSearchParams) bool { return params.Agent == agent && params.Human.ID == "" }},
	} {
		t.Run(name, func(t *testing.T) {
			backend.searches = nil
			request := knowledgeRequest("search", 7, test.token)
			if _, err := service.SearchKnowledge(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if len(backend.searches) != 1 || !test.check(backend.searches[0]) || backend.searches[0].Now != now || backend.searches[0].Limit != 7 {
				t.Fatalf("search params = %+v", backend.searches)
			}
		})
	}

	for name, mutate := range map[string]func(*connect.Request[knowledgev1.SearchKnowledgeRequest]){
		"ambiguous identity": func(request *connect.Request[knowledgev1.SearchKnowledgeRequest]) {
			request.Header().Set("Authorization", "Bearer "+ambiguousToken)
		},
		"bearer and cookie": func(request *connect.Request[knowledgev1.SearchKnowledgeRequest]) {
			request.Header().Set("Authorization", "Bearer "+humanToken)
			request.Header().Set("Cookie", authority.BrowserSessionCookieName+"="+strings.Repeat("c", 43))
		},
		"multiple authorization": func(request *connect.Request[knowledgev1.SearchKnowledgeRequest]) {
			request.Header().Add("Authorization", "Bearer "+humanToken)
			request.Header().Add("Authorization", "Bearer "+humanToken)
		},
		"multiple browser cookies": func(request *connect.Request[knowledgev1.SearchKnowledgeRequest]) {
			request.Header().Add("Cookie", authority.BrowserSessionCookieName+"="+strings.Repeat("c", 43))
			request.Header().Add("Cookie", authority.BrowserSessionCookieName+"="+strings.Repeat("d", 43))
		},
	} {
		t.Run(name, func(t *testing.T) {
			backend.searches = nil
			request := connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "search"})
			mutate(request)
			_, err := service.SearchKnowledge(context.Background(), request)
			assertKnowledgeConnectError(t, err, connect.CodeUnauthenticated, "knowledge authentication invalid")
			if len(backend.searches) != 0 {
				t.Fatalf("unauthenticated request searched with %+v", backend.searches)
			}
		})
	}
}

func TestKnowledgeTransportMapsResultsAndErrorsQuietly(t *testing.T) {
	token := strings.Repeat("h", 43)
	human := authoritydomain.Principal{Kind: authoritydomain.PrincipalHuman, ID: "human-id", OrganizationID: "organization-id"}
	backend := &testKnowledgeBackend{humans: map[string]authoritydomain.Principal{token: human}}
	service := newKnowledgeService(backend, "")
	service.now = func() time.Time { return time.Date(2026, 7, 23, 8, 30, 0, 0, time.UTC) }
	backend.page = store.KnowledgeSearchOutput{
		Results: []store.KnowledgeSearchResult{
			{Source: store.KnowledgeSource{Kind: store.KnowledgeSourceMessage, ID: "message-id"}, Snippet: "message snippet"},
			{Source: store.KnowledgeSource{Kind: store.KnowledgeSourceWork, ID: "work-id"}, Snippet: "work snippet"},
			{Source: store.KnowledgeSource{Kind: store.KnowledgeSourceArtifactVersion, ID: "artifact-id", Version: 9}, Snippet: "artifact snippet"},
		},
		Status: store.KnowledgeIndexReady,
	}
	response, err := service.SearchKnowledge(context.Background(), knowledgeRequest("search", 3, token))
	if err != nil {
		t.Fatal(err)
	}
	results := response.Msg.GetResults()
	if len(results) != 3 || results[0].GetCitation().GetMessage().GetMessageId() != "message-id" || results[0].GetSnippet() != "message snippet" ||
		results[1].GetCitation().GetWork().GetWorkId() != "work-id" || results[1].GetSnippet() != "work snippet" ||
		results[2].GetCitation().GetArtifactVersion().GetArtifactId() != "artifact-id" || results[2].GetCitation().GetArtifactVersion().GetVersion() != 9 ||
		results[2].GetSnippet() != "artifact snippet" ||
		response.Msg.GetStatus() != knowledgev1.KnowledgeIndexStatus_KNOWLEDGE_INDEX_STATUS_READY {
		t.Fatalf("knowledge response = %+v", response.Msg)
	}

	for status, want := range map[string]knowledgev1.KnowledgeIndexStatus{
		store.KnowledgeIndexReady:    knowledgev1.KnowledgeIndexStatus_KNOWLEDGE_INDEX_STATUS_READY,
		store.KnowledgeIndexDegraded: knowledgev1.KnowledgeIndexStatus_KNOWLEDGE_INDEX_STATUS_DEGRADED,
	} {
		t.Run("status "+status, func(t *testing.T) {
			backend.page = store.KnowledgeSearchOutput{Status: status}
			response, err := service.SearchKnowledge(context.Background(), knowledgeRequest("search", 0, token))
			if err != nil || response.Msg.GetStatus() != want {
				t.Fatalf("status response = %+v, %v", response, err)
			}
		})
	}

	private := "sqlite /private/data secret=" + strings.Repeat("z", 43)
	for name, test := range map[string]struct {
		err     error
		code    connect.Code
		message string
	}{
		"invalid":              {err: store.ErrKnowledgeSearchInvalid, code: connect.CodeInvalidArgument, message: "knowledge search input is invalid"},
		"store authentication": {err: store.ErrKnowledgeSearchUnauthenticated, code: connect.CodeUnauthenticated, message: "knowledge authentication invalid"},
		"canceled":             {err: context.Canceled, code: connect.CodeCanceled, message: "knowledge search canceled"},
		"deadline":             {err: context.DeadlineExceeded, code: connect.CodeDeadlineExceeded, message: "knowledge search deadline exceeded"},
		"backend":              {err: errors.New(private), code: connect.CodeInternal, message: "knowledge service unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			backend.searchErr = test.err
			_, err := service.SearchKnowledge(context.Background(), knowledgeRequest("search", 0, token))
			assertKnowledgeConnectError(t, err, test.code, test.message)
			if strings.Contains(err.Error(), private) {
				t.Fatalf("private backend detail leaked: %v", err)
			}
		})
	}
	backend.searchErr = nil
	backend.authErr = errors.New(private)
	backend.searches = nil
	_, err = service.SearchKnowledge(context.Background(), knowledgeRequest("search", 0, token))
	assertKnowledgeConnectError(t, err, connect.CodeInternal, "knowledge service unavailable")
	if strings.Contains(err.Error(), private) || len(backend.searches) != 0 {
		t.Fatalf("authentication backend failure leaked or searched: %v, %+v", err, backend.searches)
	}
	backend.authErr = nil

	for name, page := range map[string]store.KnowledgeSearchOutput{
		"unknown status": {Status: "future"},
		"unknown source": {Status: store.KnowledgeIndexReady, Results: []store.KnowledgeSearchResult{{Source: store.KnowledgeSource{Kind: "future", ID: private}}}},
	} {
		t.Run(name, func(t *testing.T) {
			backend.page = page
			_, err := service.SearchKnowledge(context.Background(), knowledgeRequest("search", 0, token))
			assertKnowledgeConnectError(t, err, connect.CodeInternal, "knowledge service unavailable")
			if strings.Contains(err.Error(), private) {
				t.Fatalf("private projection detail leaked: %v", err)
			}
		})
	}
}

func TestKnowledgeHTTPAcceptsHumanBrowserAndCurrentRuntime(t *testing.T) {
	t.Run("human and runtime", func(t *testing.T) {
		dataRoot := t.TempDir()
		api := openFactsAPI(t, dataRoot)
		defer api.close(t)
		client := knowledgev1connect.NewKnowledgeServiceClient(api.http.Client(), api.http.URL, ownerClientAuthorization(t, dataRoot))
		if _, err := client.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "knowledge"})); err != nil {
			t.Fatalf("human search: %v", err)
		}
		for name, request := range map[string]*knowledgev1.SearchKnowledgeRequest{
			"empty query": {},
			"query bytes": {Query: strings.Repeat("q", 257)},
			"term count":  {Query: "one two three four five six seven eight nine"},
			"term bytes":  {Query: strings.Repeat("q", 65)},
			"limit":       {Query: "knowledge", Limit: 51},
		} {
			t.Run(name, func(t *testing.T) {
				_, err := client.SearchKnowledge(context.Background(), connect.NewRequest(request))
				assertKnowledgeConnectError(t, err, connect.CodeInvalidArgument, "knowledge search input is invalid")
			})
		}
		computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
		runtimeClient := runtimev1connect.NewAgentRuntimeServiceClient(api.http.Client(), api.http.URL)
		session := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetGeneration())
		runtimeSearch := knowledgev1connect.NewKnowledgeServiceClient(api.http.Client(), api.http.URL)
		if _, err := runtimeSearch.SearchKnowledge(context.Background(), knowledgeRequest("knowledge", 0, session.GetToken())); err != nil {
			t.Fatalf("runtime search: %v", err)
		}
	})

	t.Run("browser and mixed", func(t *testing.T) {
		dataRoot := t.TempDir()
		api := openBrowserServer(t, dataRoot)
		defer api.close(t)
		credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
		if err != nil {
			t.Fatal(err)
		}
		browser := browserClient(t, api.origin, credential)
		client := knowledgev1connect.NewKnowledgeServiceClient(browser, api.origin)
		if _, err := client.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "knowledge"})); err != nil {
			t.Fatalf("browser search: %v", err)
		}
		mixed := knowledgev1connect.NewKnowledgeServiceClient(browser, api.origin, clientAuthorization(credential))
		_, err = mixed.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "knowledge"}))
		assertKnowledgeConnectError(t, err, connect.CodeUnauthenticated, "knowledge authentication invalid")
		multipleCookies := knowledgev1connect.NewKnowledgeServiceClient(browser, api.origin, connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
				request.Header().Add("Cookie", authority.BrowserSessionCookieName+"="+strings.Repeat("d", 43))
				return next(ctx, request)
			}
		})))
		_, err = multipleCookies.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "knowledge"}))
		assertKnowledgeConnectError(t, err, connect.CodeUnauthenticated, "knowledge authentication invalid")
	})
}

type testKnowledgeBackend struct {
	humans    map[string]authoritydomain.Principal
	agents    map[string]authorityapp.RuntimeAuthentication
	browsers  map[string]authoritydomain.Principal
	authErr   error
	page      store.KnowledgeSearchOutput
	searchErr error
	searches  []store.KnowledgeSearchParams
}

func (b *testKnowledgeBackend) AuthenticateHuman(_ context.Context, token string) (authoritydomain.Principal, error) {
	if b.authErr != nil {
		return authoritydomain.Principal{}, b.authErr
	}
	if human, ok := b.humans[token]; ok {
		return human, nil
	}
	return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
}

func (b *testKnowledgeBackend) AuthenticateAgentRuntimeSession(_ context.Context, token string, _ time.Time) (authorityapp.RuntimeAuthentication, error) {
	if b.authErr != nil {
		return authorityapp.RuntimeAuthentication{}, b.authErr
	}
	if agent, ok := b.agents[token]; ok {
		return agent, nil
	}
	return authorityapp.RuntimeAuthentication{}, authorityapp.ErrRuntimeUnauthenticated
}

func (b *testKnowledgeBackend) AuthenticateBrowserSession(_ context.Context, token string, _ time.Time) (authoritydomain.Principal, error) {
	if b.authErr != nil {
		return authoritydomain.Principal{}, b.authErr
	}
	if human, ok := b.browsers[token]; ok {
		return human, nil
	}
	return authoritydomain.Principal{}, authoritydomain.ErrPermissionDenied
}

func (b *testKnowledgeBackend) SearchKnowledge(_ context.Context, params store.KnowledgeSearchParams) (store.KnowledgeSearchOutput, error) {
	b.searches = append(b.searches, params)
	return b.page, b.searchErr
}

func knowledgeRequest(query string, limit uint32, token string) *connect.Request[knowledgev1.SearchKnowledgeRequest] {
	request := connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: query, Limit: limit})
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func assertKnowledgeConnectError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	if err == nil || connect.CodeOf(err) != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Message() != message {
		t.Fatalf("error message = %v, want %q", err, message)
	}
}
