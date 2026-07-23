package work

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestServiceUsesSharedAuthenticationOneOfForReadsAndMutations(t *testing.T) {
	now := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	human := store.Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: uuid.NewString()}
	agent := store.AgentRuntimeAuthentication{Principal: store.Principal{Kind: "agent", ID: uuid.NewString(), OrganizationID: human.OrganizationID}}
	humanToken := strings.Repeat("h", 43)
	runtimeToken := strings.Repeat("r", 43)
	staleToken := strings.Repeat("s", 43)
	ambiguousToken := strings.Repeat("a", 43)
	backend := &testStore{humans: map[string]store.Principal{humanToken: human}, agents: map[string]store.AgentRuntimeAuthentication{runtimeToken: agent}}
	service := New(backend, "http://127.0.0.1:18080")
	service.now = func() time.Time { return now }

	t.Run("human read", func(t *testing.T) {
		request := connect.NewRequest(&workv1.ListWorksRequest{})
		request.Header().Set("Authorization", "Bearer "+humanToken)
		if _, err := service.ListWorks(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if len(backend.listed) != 1 || backend.listed[0].Actor != human || backend.listed[0].Agent.Valid() {
			t.Fatalf("human params = %+v", backend.listed)
		}
	})

	t.Run("runtime read", func(t *testing.T) {
		backend.listed = nil
		request := connect.NewRequest(&workv1.ListWorksRequest{})
		request.Header().Set("Authorization", "Bearer "+runtimeToken)
		if _, err := service.ListWorks(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if len(backend.listed) != 1 || backend.listed[0].Actor.ID != "" || backend.listed[0].Agent != agent {
			t.Fatalf("runtime params = %+v", backend.listed)
		}
	})

	t.Run("stale runtime and mixed identity do not mutate", func(t *testing.T) {
		before := len(backend.created)
		for _, token := range []string{staleToken, ambiguousToken} {
			request := createRequest(t)
			request.Header().Set("Authorization", "Bearer "+token)
			if token == ambiguousToken {
				backend.humans[token] = human
				backend.agents[token] = agent
			}
			_, err := service.CreateWork(context.Background(), request)
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("%s error = %v", token, err)
			}
		}
		if len(backend.created) != before {
			t.Fatalf("unauthenticated mutation reached store: %+v", backend.created)
		}
	})

	t.Run("runtime mutation has only verified principal", func(t *testing.T) {
		request := createRequest(t)
		request.Header().Set("Authorization", "Bearer "+runtimeToken)
		if _, err := service.CreateWork(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if got := backend.created[len(backend.created)-1]; got.Actor != agent.Principal || got.Actor.OrganizationID != human.OrganizationID {
			t.Fatalf("runtime create = %+v", got)
		}
	})
}

func TestServiceRejectsEveryMutationBeforeStore(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	human := store.Principal{Kind: "human", ID: uuid.NewString(), OrganizationID: uuid.NewString()}
	runtimeToken := strings.Repeat("r", 43)
	staleToken := strings.Repeat("s", 43)
	revokedToken := strings.Repeat("v", 43)
	browserToken := strings.Repeat("b", 43)
	backend := &testStore{
		humans:        map[string]store.Principal{},
		agents:        map[string]store.AgentRuntimeAuthentication{runtimeToken: {Principal: store.Principal{Kind: "agent", ID: uuid.NewString(), OrganizationID: human.OrganizationID}}},
		browsers:      map[string]store.Principal{browserToken: human},
		runtimeErrors: map[string]error{revokedToken: authorityapp.ErrRuntimeUnauthenticated},
	}
	service := New(backend, "http://127.0.0.1:18080")
	service.now = func() time.Time { return now }
	browserContext := workBrowserMutationContext(t, "http://127.0.0.1:18080")

	mutations := []struct {
		name string
		call func(context.Context, *Service, http.Header) error
	}{
		{"create", func(ctx context.Context, service *Service, header http.Header) error {
			request := createRequest(t)
			applyWorkHeaders(request.Header(), header)
			_, err := service.CreateWork(ctx, request)
			return err
		}},
		{"assign", func(ctx context.Context, service *Service, header http.Header) error {
			request := connect.NewRequest(&workv1.AssignWorkRequest{RequestId: uuid.NewString(), WorkId: uuid.NewString(), AgentId: uuid.NewString(), Role: workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR})
			applyWorkHeaders(request.Header(), header)
			_, err := service.AssignWork(ctx, request)
			return err
		}},
		{"transition", func(ctx context.Context, service *Service, header http.Header) error {
			request := connect.NewRequest(&workv1.TransitionWorkRequest{RequestId: uuid.NewString(), WorkId: uuid.NewString(), ToState: workv1.WorkState_WORK_STATE_BLOCKED, Reason: "blocked"})
			applyWorkHeaders(request.Header(), header)
			_, err := service.TransitionWork(ctx, request)
			return err
		}},
		{"request approval", func(ctx context.Context, service *Service, header http.Header) error {
			request := connect.NewRequest(&workv1.RequestApprovalRequest{RequestId: uuid.NewString(), WorkId: uuid.NewString(), Question: "approve?"})
			applyWorkHeaders(request.Header(), header)
			_, err := service.RequestApproval(ctx, request)
			return err
		}},
		{"resolve approval", func(ctx context.Context, service *Service, header http.Header) error {
			request := connect.NewRequest(&workv1.ResolveApprovalRequest{RequestId: uuid.NewString(), ApprovalId: uuid.NewString(), Decision: workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_APPROVED})
			applyWorkHeaders(request.Header(), header)
			_, err := service.ResolveApproval(ctx, request)
			return err
		}},
	}
	cases := []struct {
		name   string
		ctx    context.Context
		header http.Header
		code   connect.Code
	}{
		{"stale runtime", context.Background(), http.Header{"Authorization": {"Bearer " + staleToken}}, connect.CodeUnauthenticated},
		{"revoked runtime", context.Background(), http.Header{"Authorization": {"Bearer " + revokedToken}}, connect.CodeUnauthenticated},
		{"mixed bearer cookie", context.Background(), http.Header{"Authorization": {"Bearer " + runtimeToken}, "Cookie": {authority.BrowserSessionCookieName + "=" + browserToken}}, connect.CodeUnauthenticated},
		{"browser missing origin", browserContext, http.Header{"Cookie": {authority.BrowserSessionCookieName + "=" + browserToken}}, connect.CodePermissionDenied},
		{"browser bad origin", browserContext, http.Header{"Cookie": {authority.BrowserSessionCookieName + "=" + browserToken}, "Origin": {"http://localhost:18080"}}, connect.CodePermissionDenied},
	}
	for _, mutation := range mutations {
		for _, test := range cases {
			t.Run(mutation.name+"/"+test.name, func(t *testing.T) {
				before := backend.mutationCalls()
				err := mutation.call(test.ctx, service, test.header)
				if connect.CodeOf(err) != test.code {
					t.Fatalf("error = %v, want %s", err, test.code)
				}
				if got := backend.mutationCalls(); got != before {
					t.Fatalf("unauthenticated mutation reached store: got %d, want %d", got, before)
				}
			})
		}
	}
}

func applyWorkHeaders(destination, source http.Header) {
	for key, values := range source {
		destination[key] = append([]string(nil), values...)
	}
}

func workBrowserMutationContext(t *testing.T, origin string) context.Context {
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

func TestServiceErrorsRemainQuietAndCursorIsFixed(t *testing.T) {
	for name, test := range map[string]struct {
		err     error
		code    connect.Code
		message string
	}{
		"canceled":         {context.Canceled, connect.CodeCanceled, "work request canceled"},
		"deadline":         {context.DeadlineExceeded, connect.CodeDeadlineExceeded, "work request deadline exceeded"},
		"cursor":           {store.ErrWorkCursorUnavailable, connect.CodeFailedPrecondition, "cursor unavailable"},
		"backend":          {errors.New("sqlite /private/work secret"), connect.CodeInternal, "work service unavailable"},
		"runtime":          {authorityapp.ErrRuntimeUnauthenticated, connect.CodeUnauthenticated, "work authentication invalid"},
		"work not found":   {store.ErrWorkNotFound, connect.CodeNotFound, "work fact not found"},
		"approval missing": {store.ErrWorkApprovalNotFound, connect.CodeNotFound, "work fact not found"},
		"permission":       {store.ErrPermissionDenied, connect.CodePermissionDenied, "work action denied"},
		"conflict":         {store.ErrWorkRequestConflict, connect.CodeAlreadyExists, "work request conflicts with committed request"},
		"transition":       {store.ErrWorkTransitionInvalid, connect.CodeFailedPrecondition, "work state conflict"},
		"invalid":          {store.ErrWorkInvalid, connect.CodeInvalidArgument, "work input is invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			err := serviceError(test.err)
			if connect.CodeOf(err) != test.code || err.Error() != test.code.String()+": "+test.message || strings.Contains(err.Error(), "sqlite") || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func createRequest(t *testing.T) *connect.Request[workv1.CreateWorkRequest] {
	t.Helper()
	spaceID := uuid.NewString()
	return connect.NewRequest(&workv1.CreateWorkRequest{RequestId: uuid.NewString(), SourceMessageId: uuid.NewString(), SourceSpaceId: spaceID, SourceTarget: &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: spaceID}}, SourceTargetSequence: 1, Goal: "unit work", AcceptanceCriteria: []string{"done"}})
}

type testStore struct {
	humans        map[string]store.Principal
	agents        map[string]store.AgentRuntimeAuthentication
	browsers      map[string]store.Principal
	runtimeErrors map[string]error
	listed        []store.ListWorkPageParams
	created       []store.WorkCreateParams
	assigned      []store.AssignWorkParams
	transitioned  []store.TransitionWorkParams
	requested     []store.RequestWorkApprovalParams
	resolved      []store.ResolveWorkApprovalParams
}

func (s *testStore) AuthenticateHuman(_ context.Context, token string) (store.Principal, error) {
	if value, ok := s.humans[token]; ok {
		return value, nil
	}
	return store.Principal{}, store.ErrPermissionDenied
}
func (s *testStore) AuthenticateAgentRuntimeSession(_ context.Context, token string, _ time.Time) (store.AgentRuntimeAuthentication, error) {
	if err := s.runtimeErrors[token]; err != nil {
		return store.AgentRuntimeAuthentication{}, err
	}
	if value, ok := s.agents[token]; ok {
		return value, nil
	}
	return store.AgentRuntimeAuthentication{}, authorityapp.ErrRuntimeUnauthenticated
}

func (s *testStore) AuthenticateBrowserSession(_ context.Context, token string, _ time.Time) (store.Principal, error) {
	if value, ok := s.browsers[token]; ok {
		return value, nil
	}
	return store.Principal{}, store.ErrPermissionDenied
}
func (s *testStore) ListWorkPage(_ context.Context, params store.ListWorkPageParams) (store.WorkPage, error) {
	s.listed = append(s.listed, params)
	return store.WorkPage{}, nil
}
func (*testStore) GetWorkDetail(context.Context, store.WorkReadParams) (store.WorkDetail, error) {
	return store.WorkDetail{}, nil
}
func (s *testStore) CreateWork(_ context.Context, params store.WorkCreateParams) (store.Work, error) {
	s.created = append(s.created, params)
	return store.Work{ID: uuid.NewString(), OrganizationID: params.Actor.OrganizationID, RootWorkID: uuid.NewString(), SourceMessageID: params.SourceMessageID, SourceSpaceID: params.SourceSpaceID, SourceTarget: params.SourceTarget, SourceTargetSequence: params.SourceTargetSequence, TeamSpaceID: uuid.NewString(), Goal: params.Goal, State: store.WorkStateOpen, Creator: params.Actor, CreatedAt: params.Now, UpdatedAt: params.Now, StateChangedAt: params.Now}, nil
}
func (s *testStore) AssignWork(_ context.Context, params store.AssignWorkParams) (store.WorkAssignment, error) {
	s.assigned = append(s.assigned, params)
	return store.WorkAssignment{}, nil
}
func (s *testStore) TransitionWork(_ context.Context, params store.TransitionWorkParams) (store.Work, error) {
	s.transitioned = append(s.transitioned, params)
	return store.Work{}, nil
}
func (s *testStore) RequestWorkApproval(_ context.Context, params store.RequestWorkApprovalParams) (store.WorkApproval, error) {
	s.requested = append(s.requested, params)
	return store.WorkApproval{}, nil
}
func (s *testStore) ResolveWorkApproval(_ context.Context, params store.ResolveWorkApprovalParams) (store.WorkApproval, error) {
	s.resolved = append(s.resolved, params)
	return store.WorkApproval{}, nil
}

func (s *testStore) mutationCalls() int {
	return len(s.created) + len(s.assigned) + len(s.transitioned) + len(s.requested) + len(s.resolved)
}

var _ workStore = (*testStore)(nil)
