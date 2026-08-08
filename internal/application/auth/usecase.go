package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SessionTTL is how long an issued session stays valid without re-authenticating.
const SessionTTL = 24 * time.Hour

// PendingTOTPTTL is how long a QR-enrollment secret stays valid before it must be re-started —
// long enough to scan a QR code and type in the confirmation code, short enough that an
// abandoned enrollment doesn't linger as valid state forever.
const PendingTOTPTTL = 10 * time.Minute

// sessionKeyPrefix and pendingTOTPKeyPrefix namespace tokens in the shared Redis keyspace (also
// used for caching elsewhere in Atlas) so they can't collide with unrelated keys.
const (
	sessionKeyPrefix     = "auth:session:"
	pendingTOTPKeyPrefix = "auth:pending-totp:"
	loginFailureKey      = "auth:login-failures"
)

// maxLoginFailures and loginLockoutWindow bound brute-forcing the single admin password: Atlas
// is a single-account tool (see UseCase's doc comment below), so the counter is global rather
// than per-IP — there is no legitimate scenario where distinguishing callers by IP matters, and
// a global counter needs no reverse-proxy/X-Forwarded-For trust decision to be correct.
const (
	maxLoginFailures   = 5
	loginLockoutWindow = 15 * time.Minute
)

// ErrInvalidCredentials is deliberately uniform across every possible failure (wrong username,
// wrong password, wrong/missing TOTP code) so a login response never leaks which specific
// factor was wrong.
var ErrInvalidCredentials = errors.New("invalid username, password, or verification code")

// ErrTooManyAttempts is returned instead of ErrInvalidCredentials once the lockout threshold is
// hit — distinct on purpose, so the one legitimate operator knows to wait rather than assuming
// their password changed.
var ErrTooManyAttempts = errors.New("too many failed login attempts — wait a few minutes and try again")

// TOTPStore persists the account's 2FA settings — implemented by
// infrastructure/persistence.TimescaleDB (migrations/008's auth_settings table). Unlike
// SessionStore, this needs to be durable (not just cached): losing it would silently disable a
// user's 2FA.
type TOTPStore interface {
	GetTOTP() (secret string, enabled bool, err error)
	SetTOTP(secret string, enabled bool) error
}

// UseCase implements Atlas's single-admin-account authentication (see docs/deployment.md and
// implementation-plan.md's Phase 8 note) — deliberately not a multi-user system: Atlas is a
// personal decision-support tool, not a product with accounts, so there's no users table for
// identity, just one configured operator. Username/PasswordHash come from configuration (env
// vars) — see cmd/generate-admin-credentials — matching this codebase's existing non-fatal-
// optional pattern (MODEL_PATH/ML, DiscordWebhookURL/Notifications): Atlas runs with no auth
// gate at all, exactly as before this feature existed, when these are unset — see Enabled.
//
// 2FA (TOTP) is deliberately NOT part of that static config: it's enabled/disabled from the
// dashboard after logging in, via QR-code enrollment with a real authenticator app (see
// BeginTOTPEnrollment/ConfirmTOTPEnrollment) — TOTPStore holds that mutable, durable state.
type UseCase struct {
	Username     string
	PasswordHash string
	Sessions     SessionStore
	TOTP         TOTPStore
}

func NewUseCase(username, passwordHash string, sessions SessionStore, totp TOTPStore) *UseCase {
	return &UseCase{Username: username, PasswordHash: passwordHash, Sessions: sessions, TOTP: totp}
}

// Enabled reports whether auth is actually configured. Safe to call on a nil *UseCase (matching
// this codebase's other optional-dependency fields like Orchestrator.ML) so callers don't need
// a separate nil check before asking.
func (u *UseCase) Enabled() bool {
	return u != nil && u.Username != "" && u.PasswordHash != ""
}

// TOTPStatus reports whether 2FA is currently enabled — for the account settings page.
func (u *UseCase) TOTPStatus() (enabled bool, err error) {
	_, enabled, err = u.TOTP.GetTOTP()
	return enabled, err
}

// Login verifies username/password and, if 2FA is currently enabled (per TOTPStore, not static
// config), totpCode — issuing a new session only when every required factor succeeds. Repeated
// failures lock out further attempts for loginLockoutWindow — see maxLoginFailures.
func (u *UseCase) Login(ctx context.Context, username, password, totpCode string) (token string, err error) {
	lockedOut, err := u.tooManyRecentFailures(ctx)
	if err != nil {
		return "", fmt.Errorf("check login lockout: %w", err)
	}
	if lockedOut {
		return "", ErrTooManyAttempts
	}
	if !u.verifyPassword(username, password) {
		return "", u.recordLoginFailure(ctx)
	}
	secret, enabled, err := u.TOTP.GetTOTP()
	if err != nil {
		return "", fmt.Errorf("load totp settings: %w", err)
	}
	if enabled && !ValidateTOTP(secret, totpCode, time.Now()) {
		return "", u.recordLoginFailure(ctx)
	}
	_ = u.Sessions.Delete(ctx, loginFailureKey)
	return u.createSession(ctx)
}

func (u *UseCase) tooManyRecentFailures(ctx context.Context) (bool, error) {
	value, found, err := u.Sessions.Get(ctx, loginFailureKey)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return false, nil
	}
	return count >= maxLoginFailures, nil
}

// recordLoginFailure increments the global failure counter and always returns
// ErrInvalidCredentials — callers just return its result, so a Redis error incrementing the
// counter never masks the real reason Login failed.
func (u *UseCase) recordLoginFailure(ctx context.Context) error {
	if _, err := u.Sessions.Increment(ctx, loginFailureKey, loginLockoutWindow); err != nil {
		return fmt.Errorf("record login failure: %w", err)
	}
	return ErrInvalidCredentials
}

// verifyPassword uses bcrypt's constant-time comparison. On a username mismatch it still runs
// bcrypt against the real configured hash (with an unrelated password) rather than
// short-circuiting, so a wrong username can't be distinguished from a wrong password by timing.
func (u *UseCase) verifyPassword(username, password string) bool {
	if !u.Enabled() {
		return false
	}
	if username != u.Username {
		_ = bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

func (u *UseCase) createSession(ctx context.Context) (token string, err error) {
	token, err = randomToken()
	if err != nil {
		return "", err
	}
	if err := u.Sessions.Set(ctx, sessionKeyPrefix+token, u.Username, SessionTTL); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}
	return token, nil
}

// ValidateSession reports whether token is a live, unexpired session. Always false when auth
// isn't Enabled — there is nothing valid to check a token against.
func (u *UseCase) ValidateSession(ctx context.Context, token string) bool {
	if !u.Enabled() || token == "" {
		return false
	}
	_, found, err := u.Sessions.Get(ctx, sessionKeyPrefix+token)
	return err == nil && found
}

// DeleteSession invalidates a session (logout) — best-effort, a missing/already-expired key is
// not an error.
func (u *UseCase) DeleteSession(ctx context.Context, token string) error {
	return u.Sessions.Delete(ctx, sessionKeyPrefix+token)
}

// BeginTOTPEnrollment generates a new candidate secret and stashes it against the session
// (pending, in Sessions — same Redis-backed store as sessions themselves, different key
// namespace), not yet the real second factor. The dashboard renders it as a QR code
// (TOTPProvisioningURI) for an authenticator app to scan; nothing is durably activated until
// ConfirmTOTPEnrollment proves the app actually has it.
func (u *UseCase) BeginTOTPEnrollment(ctx context.Context, sessionToken string) (secret string, err error) {
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", err
	}
	if err := u.Sessions.Set(ctx, pendingTOTPKeyPrefix+sessionToken, secret, PendingTOTPTTL); err != nil {
		return "", fmt.Errorf("store pending totp secret: %w", err)
	}
	return secret, nil
}

// PendingTOTPSecret returns the in-progress enrollment secret for a session, if any — used by
// both the enrollment confirmation page and its QR-code image endpoint so they render the same
// secret without generating it twice.
func (u *UseCase) PendingTOTPSecret(ctx context.Context, sessionToken string) (secret string, found bool, err error) {
	return u.Sessions.Get(ctx, pendingTOTPKeyPrefix+sessionToken)
}

// ConfirmTOTPEnrollment verifies code against the session's pending secret and, if valid,
// durably activates it as the real second factor via TOTPStore — proving the operator's
// authenticator app actually has the secret before 2FA starts being enforced on login, not just
// that a QR code was displayed on screen once.
func (u *UseCase) ConfirmTOTPEnrollment(ctx context.Context, sessionToken, code string) error {
	secret, found, err := u.Sessions.Get(ctx, pendingTOTPKeyPrefix+sessionToken)
	if err != nil {
		return err
	}
	if !found || !ValidateTOTP(secret, code, time.Now()) {
		return ErrInvalidCredentials
	}
	if err := u.TOTP.SetTOTP(secret, true); err != nil {
		return err
	}
	return u.Sessions.Delete(ctx, pendingTOTPKeyPrefix+sessionToken)
}

// DisableTOTP requires the current password again — a real re-authentication check, not just
// "does a valid session cookie exist" — before turning off the second factor, since doing so
// weakens the account.
func (u *UseCase) DisableTOTP(currentPassword string) error {
	if !u.verifyPassword(u.Username, currentPassword) {
		return ErrInvalidCredentials
	}
	return u.TOTP.SetTOTP("", false)
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
