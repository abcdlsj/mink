package websession

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
	organizationapp "github.com/abcdlsj/sumi/internal/organization/application"
	"github.com/abcdlsj/sumi/internal/store"
)

const (
	SessionPath     = "/auth/session"
	LogoutPath      = "/auth/logout"
	LocalStatusPath = "/auth/local"
	LocalSetupPath  = "/auth/local/setup"
	LocalLoginPath  = "/auth/local/login"
)

var opaqueTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type sessionStore interface {
	AuthenticateBrowserSession(context.Context, string, time.Time) (authoritydomain.Principal, error)
	RevokeBrowserSession(context.Context, string, time.Time) error
	GetHuman(context.Context, string) (organizationapp.Human, error)
	FirstOwnerRegistrationRequired(context.Context) (bool, error)
	RegisterFirstOwner(context.Context, authorityapp.RegisterFirstOwnerCommand) (store.AuthorityBootstrap, error)
	GetLocalAccount(context.Context, string) (authorityapp.LocalAccount, error)
	CreateBrowserSession(context.Context, authorityapp.CreateBrowserSessionCommand) error
}

type Config struct {
	Origin             string
	SessionTTL         time.Duration
	Now                func() time.Time
	Random             io.Reader
	passwordParameters localauth.PasswordParameters
}

type Service struct {
	store              sessionStore
	origin             string
	secure             bool
	sessionTTL         time.Duration
	now                func() time.Time
	random             io.Reader
	passwordParameters localauth.PasswordParameters
	loginFailures      *loginFailureGuard
	passwordSlots      chan struct{}
	handler            http.Handler
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
	if config.SessionTTL == 0 {
		config.SessionTTL = 12 * time.Hour
	}
	if config.SessionTTL <= 0 || config.SessionTTL > 12*time.Hour {
		return nil, authorityapp.ErrBrowserSessionInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.passwordParameters == (localauth.PasswordParameters{}) {
		config.passwordParameters = localauth.DefaultPasswordParameters()
	}
	if !config.passwordParameters.Valid() {
		return nil, authorityapp.ErrLocalAccountInvalid
	}
	service := &Service{
		store: database, origin: config.Origin, secure: secure,
		sessionTTL: config.SessionTTL,
		now:        config.Now, random: config.Random,
		passwordParameters: config.passwordParameters,
		loginFailures:      newLoginFailureGuard(),
		passwordSlots:      make(chan struct{}, 2),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+SessionPath, service.getSession)
	mux.HandleFunc("POST "+LogoutPath, service.logout)
	mux.HandleFunc("GET "+LocalStatusPath, service.localStatus)
	mux.HandleFunc("POST "+LocalSetupPath, service.localSetup)
	mux.HandleFunc("POST "+LocalLoginPath, service.localLogin)
	service.handler = mux
	return service, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Service) getSession(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !validAuthRequest(r) || r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 0 {
		writeStatus(w, http.StatusUnauthorized)
		return
	}
	token, ok := sessionToken(r)
	if !ok {
		writeStatus(w, http.StatusUnauthorized)
		return
	}
	principal, err := s.store.AuthenticateBrowserSession(r.Context(), token, s.now())
	if err != nil {
		writeStatus(w, http.StatusUnauthorized)
		return
	}
	human, err := s.store.GetHuman(r.Context(), principal.ID)
	if err != nil || human.Status != "active" {
		writeStatus(w, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessionResponse{Human: sessionHuman{ID: human.ID, Name: human.Name}})
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if len(r.Header.Values("Authorization")) != 0 {
		writeStatus(w, http.StatusUnauthorized)
		return
	}
	if !validAuthRequest(r) || r.URL.RawQuery != "" || r.Header.Get("Origin") != s.origin || len(r.Header.Values("Origin")) != 1 {
		writeStatus(w, http.StatusForbidden)
		return
	}
	token, ok := sessionToken(r)
	if !ok {
		writeStatus(w, http.StatusUnauthorized)
		return
	}
	now := s.now()
	if err := s.store.RevokeBrowserSession(r.Context(), token, now); err != nil {
		http.SetCookie(w, s.sessionCookie("", time.Unix(1, 0), -1))
		writeStatus(w, http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, s.sessionCookie("", time.Unix(1, 0), -1))
	w.WriteHeader(http.StatusNoContent)
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

func validAuthRequest(r *http.Request) bool {
	return authority.BrowserRequestAllowed(r.Context())
}

func sessionToken(r *http.Request) (string, bool) {
	cookies := r.CookiesNamed(authority.BrowserSessionCookieName)
	if len(cookies) != 1 || !opaqueTokenPattern.MatchString(cookies[0].Value) {
		return "", false
	}
	return cookies[0].Value, true
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func writeStatus(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}
