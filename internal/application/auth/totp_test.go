package auth

import (
	"strings"
	"testing"
	"time"
)

// rfc6238TestSecret is RFC 6238 Appendix B's SHA1 test seed, "12345678901234567890" (ASCII
// bytes used directly as the HMAC key), base32-encoded since ValidateTOTP/generateTOTP expect a
// base32 secret like a real authenticator app would provide.
const rfc6238TestSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// TestGenerateTOTPMatchesRFC6238Vectors checks against RFC 6238 Appendix B's official SHA1 test
// vectors (which use 8-digit truncation) — since 8-digit and 6-digit truncation both reduce the
// same underlying 31-bit integer modulo a power of ten, the last 6 digits of each 8-digit
// vector are exactly what a 6-digit implementation must produce at the same time.
func TestGenerateTOTPMatchesRFC6238Vectors(t *testing.T) {
	tests := []struct {
		unixSeconds int64
		want        string // last 6 digits of the RFC 6238 8-digit vector
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
	}
	for _, tt := range tests {
		got, err := generateTOTP(rfc6238TestSecret, time.Unix(tt.unixSeconds, 0).UTC())
		if err != nil {
			t.Fatalf("unexpected error at T=%d: %v", tt.unixSeconds, err)
		}
		if got != tt.want {
			t.Fatalf("at T=%d: expected %s, got %s", tt.unixSeconds, tt.want, got)
		}
	}
}

func TestValidateTOTPAcceptsCurrentCode(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now()
	code, err := generateTOTP(secret, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ValidateTOTP(secret, code, now) {
		t.Fatal("expected the current code to validate")
	}
}

func TestValidateTOTPToleratesClockSkew(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now()
	pastCode, err := generateTOTP(secret, now.Add(-totpStep))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ValidateTOTP(secret, pastCode, now) {
		t.Fatal("expected a code from one step in the past to still validate (±1 step skew)")
	}
}

func TestValidateTOTPRejectsStaleCode(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now()
	staleCode, err := generateTOTP(secret, now.Add(-5*totpStep))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ValidateTOTP(secret, staleCode, now) {
		t.Fatal("expected a code 5 steps stale to be rejected")
	}
}

func TestValidateTOTPRejectsWrongCode(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ValidateTOTP(secret, "000000", time.Now()) {
		t.Fatal("expected an arbitrary wrong code to be rejected (astronomically unlikely to collide)")
	}
}

func TestGenerateTOTPSecretProducesDecodableSecrets(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := decodeTOTPSecret(secret); err != nil {
		t.Fatalf("expected a self-generated secret to decode cleanly, got %v", err)
	}
}

func TestTOTPProvisioningURIIncludesSecret(t *testing.T) {
	uri := TOTPProvisioningURI("Atlas", "admin", "ABCDEFGH")
	if !strings.Contains(uri, "otpauth://totp/") || !strings.Contains(uri, "secret=ABCDEFGH") {
		t.Fatalf("expected a well-formed otpauth URI, got %s", uri)
	}
}
