package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/agent/v1/agentv1connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/authority/websession"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestBrowserSessionUsesHumanAuthorityAndLogoutKeepsPublicFacts(t *testing.T) {
	api := openBrowserServer(t, t.TempDir())
	defer api.close(t)
	ownerCredential, err := authority.ReadCredentialFile(filepath.Join(api.dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	ownerSession := browserClient(t, api.origin, ownerCredential)
	ownerReads := spacev1connect.NewCollaborationServiceClient(ownerSession, api.origin)
	if _, err := ownerReads.ListSpaces(context.Background(), connect.NewRequest(&spacev1.ListSpacesRequest{})); err != nil {
		t.Fatal(err)
	}

	bootstrap, err := api.app.store.EnsureAuthority(context.Background(), ownerCredential, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	memberCredential := "browser-no-grants-credential-abcdefghijklmnopqrstuvwxyz"
	member, err := api.app.store.CreateHuman(context.Background(), store.CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Browser No Grants", Role: "member", Credential: memberCredential, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	memberSession := browserClient(t, api.origin, memberCredential)
	memberMutations := spacev1connect.NewCollaborationServiceClient(memberSession, api.origin, originAuthorization(api.origin))
	_, err = memberMutations.CreateGroup(context.Background(), connect.NewRequest(&spacev1.CreateGroupRequest{RequestId: uuid.NewString(), Name: "Denied Browser Group"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("denied browser mutation error = %v", err)
	}
	events, err := api.app.store.ListAuditEvents(context.Background(), store.ListAuditEventsParams{
		OrganizationID: owner.OrganizationID, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	latest := events[len(events)-1]
	if latest.Actor.ID != member.ID || latest.Outcome != "denied" || latest.Action != store.AuditSpaceCreate {
		t.Fatalf("denied browser audit = %+v", latest)
	}

	logout, err := http.NewRequest(http.MethodPost, api.origin+websession.LogoutPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	logout.Header.Set("Origin", api.origin)
	response, err := ownerSession.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", response.StatusCode)
	}
	_, err = ownerReads.ListSpaces(context.Background(), connect.NewRequest(&spacev1.ListSpacesRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("collaboration after logout error = %v", err)
	}
	publicAgents := agentv1connect.NewAgentServiceClient(ownerSession, api.origin)
	if _, err := publicAgents.ListAgents(context.Background(), connect.NewRequest(&agentv1.ListAgentsRequest{})); err != nil {
		t.Fatalf("public agents after logout: %v", err)
	}
	publicComputers := computerv1connect.NewComputerServiceClient(ownerSession, api.origin)
	if _, err := publicComputers.ListComputers(context.Background(), connect.NewRequest(&computerv1.ListComputersRequest{})); err != nil {
		t.Fatalf("public computers after logout: %v", err)
	}
}

func TestBrowserSessionSurvivesServerRestart(t *testing.T) {
	dataRoot := t.TempDir()
	origin := "http://127.0.0.1:18080"
	app, err := New(context.Background(), Config{DataRoot: dataRoot, BrowserOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	cookie := directBrowserSession(t, app.Handler(), origin, credential)
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	app, err = New(context.Background(), Config{DataRoot: dataRoot, BrowserOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	request := httptest.NewRequest(http.MethodGet, origin+websession.SessionPath, nil)
	request.RemoteAddr = "127.0.0.1:42000"
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restarted browser session status = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLocalAccountLoginSurvivesServerRestart(t *testing.T) {
	dataRoot := t.TempDir()
	origin := "http://127.0.0.1:18080"
	app, err := New(context.Background(), Config{DataRoot: dataRoot, BrowserOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		t.Fatal(err)
	}
	setupBody, err := json.Marshal(map[string]string{
		"username": "owner", "password": "correct horse battery staple", "bootstrap_credential": credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, origin+websession.LocalSetupPath, bytes.NewReader(setupBody))
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || len(recorder.Result().Cookies()) != 1 {
		t.Fatalf("local setup = %d %s %v", recorder.Code, recorder.Body.String(), recorder.Result().Cookies())
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	app, err = New(context.Background(), Config{DataRoot: dataRoot, BrowserOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	loginBody, err := json.Marshal(map[string]string{
		"username": "owner", "password": "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, origin+websession.LocalLoginPath, bytes.NewReader(loginBody))
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	cookies := recorder.Result().Cookies()
	if recorder.Code != http.StatusOK || len(cookies) != 1 {
		t.Fatalf("restarted local login = %d %s %v", recorder.Code, recorder.Body.String(), cookies)
	}
	request = httptest.NewRequest(http.MethodGet, origin+websession.SessionPath, nil)
	request.RemoteAddr = "127.0.0.1:42000"
	request.AddCookie(cookies[0])
	recorder = httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"name":"Owner"`)) {
		t.Fatalf("restarted local session = %d %s", recorder.Code, recorder.Body.String())
	}
}

type browserServer struct {
	app      *Server
	server   *http.Server
	listener net.Listener
	origin   string
	dataRoot string
}

func openBrowserServer(t *testing.T, dataRoot string) *browserServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	app, err := New(context.Background(), Config{DataRoot: dataRoot, BrowserOrigin: origin})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return &browserServer{app: app, server: httpServer, listener: listener, origin: origin, dataRoot: dataRoot}
}

func (api *browserServer) close(t *testing.T) {
	t.Helper()
	if err := api.server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := api.app.Close(); err != nil {
		t.Fatal(err)
	}
}

func browserClient(t *testing.T, origin, credential string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequest(http.MethodPost, origin+websession.CreateHandoffPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var handoff struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(response.Body).Decode(&handoff); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("handoff creation = %d", response.StatusCode)
	}
	response, err = client.Get(origin + handoff.Path)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
		t.Fatalf("handoff consumption = %d %v", response.StatusCode, response.Header)
	}
	return client
}

func directBrowserSession(t *testing.T, handler http.Handler, origin, credential string) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, origin+websession.CreateHandoffPath, nil)
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var handoff struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, origin+handoff.Path, nil)
	request.RemoteAddr = "127.0.0.1:42000"
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("browser session cookies = %+v", cookies)
	}
	return cookies[0]
}

func originAuthorization(origin string) connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Origin", origin)
			return next(ctx, request)
		}
	}))
}
