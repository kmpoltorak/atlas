package auth

import (
	"errors"
	"testing"
)

// A session cookie proves somebody logged in at some point. It does not prove the account holder
// is the one wiping the database right now, which is the question re-authentication asks.
func TestReauthenticateRequiresTheCurrentPassword(t *testing.T) {
	const password = "correct-horse-battery-staple"
	uc := newTestUseCase(t, password)

	if err := uc.Reauthenticate("wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected a wrong password to be rejected, got %v", err)
	}
	if err := uc.Reauthenticate(""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected an empty password to be rejected, got %v", err)
	}
	if err := uc.Reauthenticate(password); err != nil {
		t.Fatalf("the correct password must re-authenticate, got %v", err)
	}
}

// With no login gate configured there is no password to check and no account to protect, so
// asking for one would be an unanswerable prompt rather than a safeguard.
func TestReauthenticatePassesWhenAuthIsDisabled(t *testing.T) {
	uc := &UseCase{}

	if uc.Enabled() {
		t.Fatal("a UseCase with no credentials must report auth disabled")
	}
	if err := uc.Reauthenticate(""); err != nil {
		t.Fatalf("expected no re-authentication requirement with auth disabled, got %v", err)
	}
}
