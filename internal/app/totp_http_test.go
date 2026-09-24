package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	authUC "atlas/internal/application/auth"
	decisionUC "atlas/internal/application/decision"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/config"
	"atlas/internal/observability"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type memorySessions struct {
	mu     sync.Mutex
	values map[string]string
}

func (m *memorySessions) Set(_ context.Context, key, value string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

func (m *memorySessions) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[key]
	return value, ok, nil
}

func (m *memorySessions) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

func (m *memorySessions) Increment(_ context.Context, key string, _ time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, _ := strconv.ParseInt(m.values[key], 10, 64)
	n++
	m.values[key] = strconv.FormatInt(n, 10)
	return n, nil
}

type memoryTOTP struct {
	mu      sync.Mutex
	secret  string
	enabled bool
}

func (m *memoryTOTP) GetTOTP() (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secret, m.enabled, nil
}

func (m *memoryTOTP) SetTOTP(secret string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secret, m.enabled = secret, enabled
	return nil
}

func newTOTPTestApp(t *testing.T) (http.Handler, *memorySessions, *memoryTOTP) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	sessions := &memorySessions{values: map[string]string{}}
	totp := &memoryTOTP{}
	app := &Application{
		Config:       config.Config{},
		Metrics:      observability.NewMetrics(),
		Logger:       zap.NewNop(),
		Auth:         authUC.NewUseCase("admin", string(hash), sessions, totp),
		Orchestrator: decisionUC.NewOrchestrator(nil, nil, nil),
		Portfolio:    portfolioUC.NewUseCase(),
		Leader:       newLeader(),
	}
	return app.NewHandler(), sessions, totp
}

func sendForm(handler http.Handler, method, path, session string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if session != "" {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func sessionFrom(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatalf("no session cookie in response (%d)", recorder.Code)
	return ""
}

// The review reproduced this over the use case; this drives the same attack through the actual
// endpoints, since hiding the button in the UI never protected them. A session that is already
// open — say, one an attacker opened before the owner turned 2FA on — must not be able to start
// or confirm an enrollment over the active secret.
func TestTOTPEndpointsRefuseToReplaceAnActiveSecret(t *testing.T) {
	handler, sessions, totp := newTOTPTestApp(t)
	session := sessionFrom(t, sendForm(handler, http.MethodPost, "/login", "",
		url.Values{"username": {"admin"}, "password": {"correct-horse"}}))
	if err := totp.SetTOTP("JBSWY3DPEHPK3PXP", true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	setup := sendForm(handler, http.MethodGet, "/dashboard/account/totp/setup", session, nil)
	if !strings.Contains(setup.Body.String(), "already enabled") {
		t.Fatalf("setup must refuse while 2FA is active, got %d: %s", setup.Code, setup.Body.String())
	}
	if _, found, _ := sessions.Get(context.Background(), "auth:pending-totp:"+session); found {
		t.Fatal("setup stored a new pending secret while 2FA was active")
	}

	// A confirmation posted directly, with a pending secret already in place.
	if err := sessions.Set(context.Background(), "auth:pending-totp:"+session, "KRSXG5CTMVRXEZLU", time.Minute); err != nil {
		t.Fatalf("seed: %v", err)
	}
	confirm := sendForm(handler, http.MethodPost, "/dashboard/account/totp/confirm", session, url.Values{"code": {"000000"}})
	if !strings.Contains(confirm.Body.String(), "already enabled") {
		t.Fatalf("confirm must refuse while 2FA is active, got %d", confirm.Code)
	}
	if secret, enabled, _ := totp.GetTOTP(); !enabled || secret != "JBSWY3DPEHPK3PXP" {
		t.Fatal("the active secret was replaced through the endpoint")
	}
}

// Disabling over HTTP needs the password, revokes the other session, and keeps the caller signed
// in through the replacement cookie.
func TestTOTPDisableOverHTTPRevokesOtherSessions(t *testing.T) {
	handler, _, totp := newTOTPTestApp(t)
	login := url.Values{"username": {"admin"}, "password": {"correct-horse"}}
	mine := sessionFrom(t, sendForm(handler, http.MethodPost, "/login", "", login))
	other := sessionFrom(t, sendForm(handler, http.MethodPost, "/login", "", login))
	if err := totp.SetTOTP("JBSWY3DPEHPK3PXP", true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	wrong := sendForm(handler, http.MethodPost, "/dashboard/account/totp/disable", mine, url.Values{"password": {"nope"}})
	if _, enabled, _ := totp.GetTOTP(); !enabled {
		t.Fatalf("a wrong password disabled 2FA (%d)", wrong.Code)
	}

	disabled := sendForm(handler, http.MethodPost, "/dashboard/account/totp/disable", mine, url.Values{"password": {"correct-horse"}})
	if _, enabled, _ := totp.GetTOTP(); enabled {
		t.Fatalf("the correct password did not disable 2FA (%d)", disabled.Code)
	}
	replacement := sessionFrom(t, disabled)

	if got := sendForm(handler, http.MethodGet, "/dashboard/account", other, nil); got.Code != http.StatusSeeOther {
		t.Fatalf("the other session must be signed out, got %d", got.Code)
	}
	if got := sendForm(handler, http.MethodGet, "/dashboard/account", replacement, nil); got.Code != http.StatusOK {
		t.Fatalf("the replacement session must work, got %d", got.Code)
	}
}
