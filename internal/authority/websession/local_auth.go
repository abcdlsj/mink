package websession

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/google/uuid"
)

const localAuthBodyLimit = 16 * 1024

type localStatusResponse struct {
	SetupRequired bool `json:"setup_required"`
}

type localSetupRequest struct {
	Username            string `json:"username"`
	Password            string `json:"password"`
	BootstrapCredential string `json:"bootstrap_credential"`
}

type localLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Service) localStatus(response http.ResponseWriter, request *http.Request) {
	noStore(response)
	if !validAuthRequest(request) || request.URL.RawQuery != "" || len(request.Header.Values("Authorization")) != 0 {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	required, err := s.store.LocalAccountSetupRequired(request.Context())
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, localStatusResponse{SetupRequired: required})
}

func (s *Service) localSetup(response http.ResponseWriter, request *http.Request) {
	noStore(response)
	if !s.validLocalMutation(request) {
		writeStatus(response, http.StatusForbidden)
		return
	}
	var input localSetupRequest
	if !decodeLocalJSON(response, request, &input) {
		writeStatus(response, http.StatusBadRequest)
		return
	}
	username, usernameOK := normalizeLocalUsername(input.Username)
	if !usernameOK || !validNewPassword(input.Password) || !authority.ValidCredential(input.BootstrapCredential) {
		writeStatus(response, http.StatusBadRequest)
		return
	}
	required, err := s.store.LocalAccountSetupRequired(request.Context())
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	if !required {
		writeStatus(response, http.StatusConflict)
		return
	}
	bootstrapHuman, err := s.store.AuthenticateHuman(request.Context(), input.BootstrapCredential)
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	if !s.acquirePasswordSlot() {
		writeStatus(response, http.StatusTooManyRequests)
		return
	}
	defer s.releasePasswordSlot()
	digest, err := hashLocalPassword(s.random, input.Password, s.passwordParameters)
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	sessionToken, err := s.randomToken()
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	now := s.now()
	principal, err := s.store.BindBootstrapLocalAccount(request.Context(), authorityapp.BindBootstrapLocalAccountCommand{
		RequestID: uuid.NewString(), BootstrapHuman: bootstrapHuman,
		Identity: authorityapp.AuthenticationIdentity{Provider: "local", Subject: username},
		Password: digest, SessionToken: sessionToken, Now: now, SessionExpiresAt: now.Add(s.sessionTTL),
	})
	if errors.Is(err, authorityapp.ErrLocalAccountSetupDone) {
		writeStatus(response, http.StatusConflict)
		return
	}
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	human, err := s.store.GetHuman(request.Context(), principal.ID)
	if err != nil || human.Status != "active" {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	expiresAt := now.Add(s.sessionTTL)
	http.SetCookie(response, s.sessionCookie(sessionToken, expiresAt, int(s.sessionTTL.Seconds())))
	writeJSON(response, http.StatusCreated, sessionResponse{Human: sessionHuman{ID: human.ID, Name: human.Name}})
}

func (s *Service) localLogin(response http.ResponseWriter, request *http.Request) {
	noStore(response)
	if !s.validLocalMutation(request) {
		writeStatus(response, http.StatusForbidden)
		return
	}
	var input localLoginRequest
	if !decodeLocalJSON(response, request, &input) {
		writeStatus(response, http.StatusBadRequest)
		return
	}
	username, usernameOK := normalizeLocalUsername(input.Username)
	guardKey := username
	if !usernameOK {
		guardKey = "_invalid"
	}
	now := s.now()
	if !s.loginFailures.allowed(guardKey, now) {
		writeStatus(response, http.StatusTooManyRequests)
		return
	}
	if !s.acquirePasswordSlot() {
		writeStatus(response, http.StatusTooManyRequests)
		return
	}
	defer s.releasePasswordSlot()
	if !usernameOK {
		verifyDummyPassword(input.Password, s.passwordParameters)
		s.loginFailures.failed(guardKey, now)
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	account, err := s.store.GetLocalAccount(request.Context(), username)
	if errors.Is(err, authoritydomain.ErrPermissionDenied) {
		verifyDummyPassword(input.Password, s.passwordParameters)
		s.loginFailures.failed(guardKey, now)
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	if !verifyLocalPassword(input.Password, account.Password) {
		s.loginFailures.failed(guardKey, now)
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	human, err := s.store.GetHuman(request.Context(), account.Human.ID)
	if err != nil || human.Status != "active" {
		s.loginFailures.failed(guardKey, now)
		writeStatus(response, http.StatusUnauthorized)
		return
	}
	sessionToken, err := s.randomToken()
	if err != nil {
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	expiresAt := now.Add(s.sessionTTL)
	if err := s.store.CreateBrowserSession(request.Context(), authorityapp.CreateBrowserSessionCommand{
		Human: account.Human, Token: sessionToken, Now: now, ExpiresAt: expiresAt,
	}); err != nil {
		if errors.Is(err, authoritydomain.ErrPermissionDenied) {
			s.loginFailures.failed(guardKey, now)
			writeStatus(response, http.StatusUnauthorized)
			return
		}
		writeStatus(response, http.StatusInternalServerError)
		return
	}
	s.loginFailures.succeeded(guardKey)
	http.SetCookie(response, s.sessionCookie(sessionToken, expiresAt, int(s.sessionTTL.Seconds())))
	writeJSON(response, http.StatusOK, sessionResponse{Human: sessionHuman{ID: human.ID, Name: human.Name}})
}

func (s *Service) validLocalMutation(request *http.Request) bool {
	if !validAuthRequest(request) || request.URL.RawQuery != "" || len(request.Header.Values("Authorization")) != 0 ||
		request.Header.Get("Origin") != s.origin || len(request.Header.Values("Origin")) != 1 {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func (s *Service) acquirePasswordSlot() bool {
	select {
	case s.passwordSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) releasePasswordSlot() {
	<-s.passwordSlots
}

func decodeLocalJSON(response http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(response, request.Body, localAuthBodyLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
