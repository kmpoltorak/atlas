package auth

import (
	"context"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// memorySessionStore is a real, minimal in-memory SessionStore for tests — no fakes/mocks of
// business logic, just the storage boundary, matching this codebase's existing test style
// (e.g. portfolio.UseCase's in-memory Positions slice with no Repository set).
type memorySessionStore struct {
	values map[string]string
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{values: map[string]string{}}
}

func (m *memorySessionStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	m.values[key] = value
	return nil
}

func (m *memorySessionStore) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := m.values[key]
	return v, ok, nil
}

func (m *memorySessionStore) Delete(_ context.Context, key string) error {
	delete(m.values, key)
	return nil
}

func (m *memorySessionStore) Increment(_ context.Context, key string, _ time.Duration) (int64, error) {
	count, _ := strconv.Atoi(m.values[key])
	count++
	m.values[key] = strconv.Itoa(count)
	return int64(count), nil
}

// memoryTOTPStore is a real, minimal in-memory TOTPStore for tests, mirroring
// infrastructure/persistence.TimescaleDB's GetTOTP/SetTOTP contract (starts disabled/empty,
// matching "no row yet" in the real database).
type memoryTOTPStore struct {
	secret  string
	enabled bool
}

func (m *memoryTOTPStore) GetTOTP() (string, bool, error) {
	return m.secret, m.enabled, nil
}

func (m *memoryTOTPStore) SetTOTP(secret string, enabled bool) error {
	m.secret, m.enabled = secret, enabled
	return nil
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("unexpected error hashing password: %v", err)
	}
	return string(hash)
}

func newTestUseCase(t *testing.T, password string) *UseCase {
	return NewUseCase("admin", mustHash(t, password), newMemorySessionStore(), &memoryTOTPStore{})
}

func TestNotEnabledWithoutCredentials(t *testing.T) {
	uc := NewUseCase("", "", newMemorySessionStore(), &memoryTOTPStore{})
	if uc.Enabled() {
		t.Fatal("expected Enabled() to be false with no username/password configured")
	}
	if _, err := uc.Login(context.Background(), "admin", "whatever", ""); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials when not enabled, got %v", err)
	}
}

func TestNilUseCaseIsSafeAndNotEnabled(t *testing.T) {
	var uc *UseCase
	if uc.Enabled() {
		t.Fatal("expected a nil *UseCase to report Enabled() == false")
	}
}

func TestLoginSucceedsWithCorrectPasswordNoTOTP(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	if !uc.Enabled() {
		t.Fatal("expected Enabled() to be true with username+hash configured")
	}
	if enabled, err := uc.TOTPStatus(); err != nil || enabled {
		t.Fatalf("expected TOTPStatus() false with nothing enrolled, got enabled=%v err=%v", enabled, err)
	}

	token, err := uc.Login(context.Background(), "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty session token")
	}
	if !uc.ValidateSession(context.Background(), token) {
		t.Fatal("expected the freshly issued session to validate")
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	if _, err := uc.Login(context.Background(), "admin", "wrong", ""); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginRejectsWrongUsername(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	if _, err := uc.Login(context.Background(), "someone-else", "correct-horse", ""); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginLocksOutAfterRepeatedFailures(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	ctx := context.Background()

	for i := 0; i < maxLoginFailures; i++ {
		if _, err := uc.Login(ctx, "admin", "wrong", ""); err != ErrInvalidCredentials {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i, err)
		}
	}

	if _, err := uc.Login(ctx, "admin", "correct-horse", ""); err != ErrTooManyAttempts {
		t.Fatalf("expected ErrTooManyAttempts once locked out, got %v", err)
	}
}

func TestLoginResetsFailureCounterOnSuccess(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	ctx := context.Background()

	for i := 0; i < maxLoginFailures-1; i++ {
		if _, err := uc.Login(ctx, "admin", "wrong", ""); err != ErrInvalidCredentials {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", i, err)
		}
	}

	if _, err := uc.Login(ctx, "admin", "correct-horse", ""); err != nil {
		t.Fatalf("expected the correct password to still succeed below the lockout threshold: %v", err)
	}

	// The counter should have reset on success, so a fresh run of failures starts from zero
	// again rather than immediately locking out.
	for i := 0; i < maxLoginFailures-1; i++ {
		if _, err := uc.Login(ctx, "admin", "wrong", ""); err != ErrInvalidCredentials {
			t.Fatalf("post-reset attempt %d: expected ErrInvalidCredentials, got %v", i, err)
		}
	}
	if _, err := uc.Login(ctx, "admin", "correct-horse", ""); err != nil {
		t.Fatalf("expected login to still succeed after a reset counter's near-threshold failures: %v", err)
	}
}

func TestValidateSessionRejectsUnknownToken(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	if uc.ValidateSession(context.Background(), "not-a-real-token") {
		t.Fatal("expected an unknown token to be rejected")
	}
}

func TestDeleteSessionInvalidatesIt(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	token, err := uc.Login(context.Background(), "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.DeleteSession(context.Background(), token); err != nil {
		t.Fatalf("unexpected error deleting session: %v", err)
	}
	if uc.ValidateSession(context.Background(), token) {
		t.Fatal("expected the session to no longer validate after DeleteSession")
	}
}

func TestTOTPEnrollmentFlow(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	ctx := context.Background()

	// Before enrollment: login needs no TOTP code at all.
	token, err := uc.Login(ctx, "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secret, err := uc.BeginTOTPEnrollment(ctx, token)
	if err != nil {
		t.Fatalf("unexpected error starting enrollment: %v", err)
	}
	if secret == "" {
		t.Fatal("expected a non-empty pending secret")
	}
	if pending, found, err := uc.PendingTOTPSecret(ctx, token); err != nil || !found || pending != secret {
		t.Fatalf("expected PendingTOTPSecret to return the same secret, got %q found=%v err=%v", pending, found, err)
	}

	// Wrong code doesn't activate anything.
	if _, err := uc.ConfirmTOTPEnrollment(ctx, token, "000000"); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for a wrong confirmation code, got %v", err)
	}
	if enabled, _ := uc.TOTPStatus(); enabled {
		t.Fatal("expected TOTP to still be disabled after a failed confirmation")
	}

	// Correct code activates it durably.
	code, err := generateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := uc.ConfirmTOTPEnrollment(ctx, token, code); err != nil {
		t.Fatalf("unexpected error confirming enrollment: %v", err)
	}
	if enabled, err := uc.TOTPStatus(); err != nil || !enabled {
		t.Fatalf("expected TOTP to be enabled after confirmation, got enabled=%v err=%v", enabled, err)
	}

	// The pending secret is consumed, not reusable.
	if _, found, _ := uc.PendingTOTPSecret(ctx, token); found {
		t.Fatal("expected the pending secret to be cleared after successful confirmation")
	}

	// From now on, login requires the code.
	if _, err := uc.Login(ctx, "admin", "correct-horse", ""); err != ErrInvalidCredentials {
		t.Fatalf("expected login without a TOTP code to fail once 2FA is enabled, got %v", err)
	}
	liveCode, err := generateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := uc.Login(ctx, "admin", "correct-horse", liveCode); err != nil {
		t.Fatalf("unexpected error logging in with a correct TOTP code: %v", err)
	}
}

func TestDisableTOTPRequiresCurrentPassword(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	ctx := context.Background()

	token, err := uc.Login(ctx, "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	secret, err := uc.BeginTOTPEnrollment(ctx, token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	code, err := generateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	token, err = uc.ConfirmTOTPEnrollment(ctx, token, code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := uc.DisableTOTP(ctx, token, "wrong-password"); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for the wrong password, got %v", err)
	}
	if enabled, _ := uc.TOTPStatus(); !enabled {
		t.Fatal("expected TOTP to remain enabled after a failed disable attempt")
	}

	if _, err := uc.DisableTOTP(ctx, token, "correct-horse"); err != nil {
		t.Fatalf("unexpected error disabling with the correct password: %v", err)
	}
	if enabled, _ := uc.TOTPStatus(); enabled {
		t.Fatal("expected TOTP to be disabled")
	}
	if _, err := uc.Login(ctx, "admin", "correct-horse", ""); err != nil {
		t.Fatalf("expected login without a TOTP code to succeed again after disabling, got %v", err)
	}
}

// enableTOTP enrolls a fresh secret through the real flow and returns it with the session token
// the confirmation issued.
func enableTOTP(t *testing.T, uc *UseCase, token string) (secret, newToken string) {
	t.Helper()
	ctx := context.Background()
	secret, err := uc.BeginTOTPEnrollment(ctx, token)
	if err != nil {
		t.Fatalf("begin enrollment: %v", err)
	}
	code, err := generateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	newToken, err = uc.ConfirmTOTPEnrollment(ctx, token, code)
	if err != nil {
		t.Fatalf("confirm enrollment: %v", err)
	}
	return secret, newToken
}

// The review's finding: with 2FA on, a session alone could enroll a new secret over the active
// one — no password, no current code — and lock the owner out of their own account.
func TestActiveTOTPCannotBeReplacedWithoutThePassword(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	ctx := context.Background()
	token, err := uc.Login(ctx, "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	original, token := enableTOTP(t, uc, token)

	if _, err := uc.BeginTOTPEnrollment(ctx, token); err != ErrTOTPAlreadyEnabled {
		t.Fatalf("starting a second enrollment must be refused, got %v", err)
	}

	// An enrollment begun before 2FA was switched on, confirmed afterwards, must not win either.
	stale := "stale-session"
	if err := uc.Sessions.Set(ctx, pendingTOTPKeyPrefix+stale, "JBSWY3DPEHPK3PXP", PendingTOTPTTL); err != nil {
		t.Fatalf("seed pending secret: %v", err)
	}
	code, err := generateTOTP("JBSWY3DPEHPK3PXP", time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if _, err := uc.ConfirmTOTPEnrollment(ctx, stale, code); err != ErrTOTPAlreadyEnabled {
		t.Fatalf("confirming over an active secret must be refused, got %v", err)
	}
	if secret, enabled, _ := uc.TOTP.GetTOTP(); !enabled || secret != original {
		t.Fatal("the active secret was replaced")
	}
}

// Changing the second factor ends every other session and hands the caller a new one.
func TestTOTPChangesRevokeOtherSessions(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	ctx := context.Background()
	mine, err := uc.Login(ctx, "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	other, err := uc.Login(ctx, "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}

	_, mine = enableTOTP(t, uc, mine)
	if uc.ValidateSession(ctx, other) {
		t.Fatal("enabling 2FA must revoke sessions opened before it")
	}
	if !uc.ValidateSession(ctx, mine) {
		t.Fatal("the session that made the change must receive a valid replacement")
	}

	secret, _, _ := uc.TOTP.GetTOTP()
	code, err := generateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	other, err = uc.Login(ctx, "admin", "correct-horse", code)
	if err != nil {
		t.Fatalf("login with code: %v", err)
	}
	mine, err = uc.DisableTOTP(ctx, mine, "correct-horse")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if uc.ValidateSession(ctx, other) {
		t.Fatal("disabling 2FA must revoke other sessions too")
	}
	if !uc.ValidateSession(ctx, mine) {
		t.Fatal("the disabling session must receive a valid replacement")
	}
}

// Sessions stored before epochs existed hold the username; an upgrade must not log them out.
func TestSessionFromBeforeEpochsStaysValidUntilTheFirstChange(t *testing.T) {
	uc := newTestUseCase(t, "correct-horse")
	ctx := context.Background()
	if err := uc.Sessions.Set(ctx, sessionKeyPrefix+"legacy", "admin", SessionTTL); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !uc.ValidateSession(ctx, "legacy") {
		t.Fatal("a pre-upgrade session should still be valid")
	}
	mine, err := uc.Login(ctx, "admin", "correct-horse", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	enableTOTP(t, uc, mine)
	if uc.ValidateSession(ctx, "legacy") {
		t.Fatal("a pre-upgrade session must not survive a 2FA change")
	}
}
