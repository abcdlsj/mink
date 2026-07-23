package websession

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	organizationapp "github.com/abcdlsj/sumi/internal/organization/application"
)

const (
	CreateHandoffPath = "/auth/browser-handoffs"
	SessionPath       = "/auth/session"
	LogoutPath        = "/auth/logout"
	LocalStatusPath   = "/auth/local"
	LocalSetupPath    = "/auth/local/setup"
	LocalLoginPath    = "/auth/local/login"
)

var opaqueTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type sessionStore interface {
	AuthenticateHuman(context.Context, string) (authoritydomain.Principal, error)
	CreateBrowserHandoff(context.Context, authorityapp.CreateBrowserHandoffCommand) error
	ConsumeBrowserHandoff(context.Context, authorityapp.ConsumeBrowserHandoffCommand) (authoritydomain.Principal, error)
	AuthenticateBrowserSession(context.Context, string, time.Time) (authoritydomain.Principal, error)
	RevokeBrowserSession(context.Context, string, time.Time) error
	GetHuman(context.Context, string) (organizationapp.Human, error)
	LocalAccountSetupRequired(context.Context) (bool, error)
	BindBootstrapLocalAccount(context.Context, authorityapp.BindBootstrapLocalAccountCommand) (authoritydomain.Principal, error)
	GetLocalAccount(context.Context, string) (authorityapp.LocalAccount, error)
	CreateBrowserSession(context.Context, authorityapp.CreateBrowserSessionCommand) error
}

type Config struct {
	Origin             string
	HandoffTTL         time.Duration
	SessionTTL         time.Duration
	Now                func() time.Time
	Random             io.Reader
	passwordParameters passwordParameters
}

type Service struct {
	store              sessionStore
	origin             string
	secure             bool
	handoffTTL         time.Duration
	sessionTTL         time.Duration
	now                func() time.Time
	random             io.Reader
	passwordParameters passwordParameters
	loginFailures      *loginFailureGuard
	passwordSlots      chan struct{}
	handler            http.Handler
}

type handoffResponse struct {
	Path      string    `json:"path"`
	ExpiresAt time.Time `json:"expires_at"`
}

type sessionResponse struct {
	Human sessionHuman `json:"human"`
}

type sessionHuman struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func New(database sessionStore, config Config) (*Service, error) {
	secure, err := authority.BrowserOriginSecure(config.Origin)
	if err != nil || config.Origin == "" {
		return nil, authority.ErrBrowserOriginInvalid
	}
	if config.HandoffTTL == 0 {
		config.HandoffTTL = time.Minute
	}
	if config.SessionTTL == 0 {
		config.SessionTTL = 12 * time.Hour
	}
	if config.HandoffTTL <= 0 || config.HandoffTTL > time.Minute || config.SessionTTL <= 0 || config.SessionTTL > 12*time.Hour {
		return nil, authorityapp.ErrBrowserSessionInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.passwordParameters == (passwordParameters{}) {
		config.passwordParameters = defaultPasswordParameters()
	}
	if !config.passwordParameters.valid() {
		return nil, authorityapp.ErrLocalAccountInvalid
	}
	service := &Service{
		store: database, origin: config.Origin, secure: secure,
		handoffTTL: config.HandoffTTL, sessionTTL: config.SessionTTL,
		now: config.Now, random: config.Random,
		passwordParameters: config.passwordParameters,
		loginFailures:      newLoginFailureGuard(),
		passwordSlots:      make(chan struct{}, 2),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+CreateHandoffPath, service.createHandoff)
	mux.HandleFunc("GET "+CreateHandoffPath+"/{token}", service.consumeHandoff)
	mux.HandleFunc("GET "+SessionPath, service.getSession)
	mux.HandleFunc("POST "+LogoutPath, service.logout)
	mux.HandleFunc("GET "+LocalStatusPath, service.localStatus)
	mux.HandleFunc("POST "+LocalSetupPath, service.localSetup)
	mux.HandleFunc("POST "+LocalLoginPath, service.localLogin)
	service.handler = mux
	return service, nil
}

func (s *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	s.handler.ServeHTTP(response, request)
}

func (s *Service) createHandoff(response http.ResponseWriter, request *http.Request) {
	noStore(response)
	if !validAuthRequest(request) || request.URL.RawQuery != "" || !emptyBody(request) {
		writeStatus(response, http.StatusBadRequest)
		return
	}
	credential, ok := authority.BearerCredential(request.Header)
	if !ok {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	principal, err := s.store.AuthenticateHuman(request.Context(), credential)
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	token, err := s.randomToken()
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	now := s.now()
	expiresAt := now.Add(s.handoffTTL)
	if err := s.store.CreateBrowserHandoff(request.Context(), authorityapp.CreateBrowserHandoffCommand{
		Human: principal, Token: token, Now: now, ExpiresAt: expiresAt,
	}); err != nil {
		if errors.Is(err, authoritydomain.ErrPermissionDenied) {
			writeStatus(response, http.StatusUnauthorized)
			return
		}
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(response).Encode(handoffResponse{Path: CreateHandoffPath + "/" + token, ExpiresAt: expiresAt})
}

func (s *Service) consumeHandoff(response http.ResponseWriter, request *http.Request) {
	noStore(response)
	response.Header().Set("Referrer-Policy", "no-referrer")
	if validAuthRequest(request) && request.URL.RawQuery == "" {
		handoff := request.PathValue("token")
		if opaqueTokenPattern.MatchString(handoff) {
			session, err := s.randomToken()
			if err == nil {
				now := s.now()
				_, err = s.store.ConsumeBrowserHandoff(request.Context(), authorityapp.ConsumeBrowserHandoffCommand{
					HandoffToken: handoff, SessionToken: session, Now: now, SessionExpiresAt: now.Add(s.sessionTTL),
				})
				if err == nil {
					http.SetCookie(response, s.sessionCookie(session, now.Add(s.sessionTTL), int(s.sessionTTL.Seconds())))
				}
			}
		}
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (s *Service) getSession(response http.ResponseWriter, request *http.Request) {
	noStore(response)
	if !validAuthRequest(request) || request.URL.RawQuery != "" || len(request.Header.Values("Authorization")) != 0 {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	token, ok := sessionToken(request)
	if !ok {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	principal, err := s.store.AuthenticateBrowserSession(request.Context(), token, s.now())
	if err != nil {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	human, err := s.store.GetHuman(request.Context(), principal.ID)
	if err != nil || human.Status != "active" {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(sessionResponse{Human: sessionHuman{ID: human.ID, Name: human.Name}})
}

func (s *Service) logout(response http.ResponseWriter, request *http.Request) {
	noStore(response)
	if len(request.Header.Values("Authorization")) != 0 {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	if !validAuthRequest(request) || request.URL.RawQuery != "" || request.Header.Get("Origin") != s.origin || len(request.Header.Values("Origin")) != 1 {
		writeStatus(response, http.StatusForbidden)
		return
	}
	token, ok := sessionToken(request)
	if !ok {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	now := s.now()
	if err := s.store.RevokeBrowserSession(request.Context(), token, now); err != nil {
		http.SetCookie(response, s.sessionCookie("", time.Unix(1, 0), -1))
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	http.SetCookie(response, s.sessionCookie("", time.Unix(1, 0), -1))
	response.WriteHeader(http.StatusNoContent)
}

func (s *Service) randomToken() (string, error) {
	random := make([]byte, 32)
	if _, err := io.ReadFull(s.random, random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func (s *Service) sessionCookie(value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: authority.BrowserSessionCookieName, Value: value, Path: "/",
		Expires: expires, MaxAge: maxAge, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
	}
}

func validAuthRequest(request *http.Request) bool {
	return authority.BrowserRequestAllowed(request.Context())
}

func sessionToken(request *http.Request) (string, bool) {
	cookies := request.CookiesNamed(authority.BrowserSessionCookieName)
	if len(cookies) != 1 || !opaqueTokenPattern.MatchString(cookies[0].Value) {
		return "", false
	}
	return cookies[0].Value, true
}

func noStore(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
}

func writeStatus(response http.ResponseWriter, status int) {
	response.WriteHeader(status)
}

func emptyBody(request *http.Request) bool {
	return request.ContentLength == 0 && len(request.TransferEncoding) == 0
}
