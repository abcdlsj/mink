package work

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
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

func TestServiceErrorsRemainQuietAndCursorIsFixed(t *testing.T) {
	for name, test := range map[string]struct {
		err     error
		code    connect.Code
		message string
	}{
		"cursor":     {store.ErrWorkCursorUnavailable, connect.CodeFailedPrecondition, "cursor unavailable"},
		"backend":    {errors.New("sqlite /private/work secret"), connect.CodeInternal, "work service unavailable"},
		"conflict":   {store.ErrWorkRequestConflict, connect.CodeAlreadyExists, "work request conflicts with committed request"},
		"transition": {store.ErrWorkTransitionInvalid, connect.CodeFailedPrecondition, "work state conflict"},
	} {
		t.Run(name, func(t *testing.T) {
			err := serviceError(test.err)
			if connect.CodeOf(err) != test.code || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
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
	humans  map[string]store.Principal
	agents  map[string]store.AgentRuntimeAuthentication
	listed  []store.ListWorkPageParams
	created []store.WorkCreateParams
}

func (s *testStore) AuthenticateHuman(_ context.Context, token string) (store.Principal, error) {
	if value, ok := s.humans[token]; ok {
		return value, nil
	}
	return store.Principal{}, store.ErrPermissionDenied
}
func (s *testStore) AuthenticateAgentRuntimeSession(_ context.Context, token string, _ time.Time) (store.AgentRuntimeAuthentication, error) {
	if value, ok := s.agents[token]; ok {
		return value, nil
	}
	return store.AgentRuntimeAuthentication{}, authorityapp.ErrRuntimeUnauthenticated
}
func (*testStore) AuthenticateBrowserSession(context.Context, string, time.Time) (store.Principal, error) {
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
func (*testStore) AssignWork(context.Context, store.AssignWorkParams) (store.WorkAssignment, error) {
	return store.WorkAssignment{}, nil
}
func (*testStore) TransitionWork(context.Context, store.TransitionWorkParams) (store.Work, error) {
	return store.Work{}, nil
}
func (*testStore) RequestWorkApproval(context.Context, store.RequestWorkApprovalParams) (store.WorkApproval, error) {
	return store.WorkApproval{}, nil
}
func (*testStore) ResolveWorkApproval(context.Context, store.ResolveWorkApprovalParams) (store.WorkApproval, error) {
	return store.WorkApproval{}, nil
}

var _ workStore = (*testStore)(nil)
