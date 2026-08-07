package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238, built on HOTP — RFC 4226) implemented directly on stdlib crypto/hmac +
// crypto/sha1, deliberately not a third-party dependency: it's ~80 lines of well-specified,
// testable math (see totp_test.go's RFC 6238 Appendix B test vectors), consistent with this
// project's existing dependency-lean stance (sqlx/pgx/zap/prometheus/onnxruntime_go are the
// only non-stdlib runtime dependencies before this).
const (
	totpDigits = 6
	totpStep   = 30 * time.Second
	totpSkew   = 1 // tolerate ±1 time step (±30s) for clock drift between server and app
)

// base32Encoding is unpadded, uppercase — the conventional TOTP secret encoding most
// authenticator apps expect (Google Authenticator, Authy, etc.).
var base32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a random 160-bit secret, base32-encoded — RFC 4226 recommends at
// least 128 bits; 160 matches SHA-1's block size and is what most authenticator apps expect.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return base32Encoding.EncodeToString(raw), nil
}

// ValidateTOTP reports whether code matches secret's TOTP at time t, tolerating ±totpSkew time
// steps for clock drift — standard TOTP practice. Uses a constant-time comparison so a
// mistyped code doesn't leak timing information about how much of it matched.
func ValidateTOTP(secret, code string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	for skew := -totpSkew; skew <= totpSkew; skew++ {
		candidate, err := generateTOTP(secret, t.Add(time.Duration(skew)*totpStep))
		if err != nil {
			return false
		}
		if hmac.Equal([]byte(candidate), []byte(code)) {
			return true
		}
	}
	return false
}

// TOTPProvisioningURI returns the otpauth:// URI an authenticator app can import — via a QR
// code generated from this URI by any external tool, or by typing the secret in manually.
// Atlas doesn't render a QR code image itself (see cmd/generate-admin-credentials) — for a
// single personal account, printing the URI/secret as text is sufficient.
func TOTPProvisioningURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	query := url.Values{
		"secret":    {secret},
		"issuer":    {issuer},
		"algorithm": {"SHA1"},
		"digits":    {fmt.Sprintf("%d", totpDigits)},
		"period":    {fmt.Sprintf("%d", int(totpStep.Seconds()))},
	}
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// generateTOTP computes the RFC 6238 TOTP code for secret at time t: HOTP (RFC 4226) over a
// 30-second time-step counter since the Unix epoch, HMAC-SHA1, dynamically truncated to 6
// digits.
func generateTOTP(secret string, t time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix()) / uint64(totpStep.Seconds())
	return hotp(key, counter), nil
}

// hotp implements RFC 4226's HOTP algorithm: HMAC-SHA1(key, counter), dynamically truncated
// (offset = low nibble of the last byte; take 4 bytes from there, mask off the sign bit),
// reduced mod 10^totpDigits, and zero-padded to a fixed width.
func hotp(key []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, truncated%mod)
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	clean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32Encoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("decode totp secret: %w", err)
	}
	return key, nil
}
