package security

import (
	"testing"
	"time"
)

func TestTOTPCodeVerifiesWithinWindow(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	code, err := TOTPCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyTOTP(secret, code, now.Add(29*time.Second)) {
		t.Fatal("expected TOTP code to verify within adjacent window")
	}
	if VerifyTOTP(secret, "000000", now) {
		t.Fatal("unexpected invalid TOTP accepted")
	}
}

func TestRecoveryCodeHashNormalizesFormatting(t *testing.T) {
	hash := HashRecoveryCode("ABCD-EFGH-IJKL")
	if !VerifyTokenHash("ABCDEFGHIJKL", hash) {
		t.Fatal("expected normalized recovery code hash")
	}
}
