package security

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse battery" {
		t.Fatal("password stored in plain text")
	}
	if !VerifyPassword("correct horse battery", hash) {
		t.Fatal("expected password verification to pass")
	}
	if VerifyPassword("wrong horse battery", hash) {
		t.Fatal("expected password verification to fail")
	}
}

func TestWeakPasswordRejected(t *testing.T) {
	if err := ValidatePassword("password1234"); err == nil {
		t.Fatal("weak password was accepted")
	}
}

func TestValidatePasswordWithConfiguredMinimum(t *testing.T) {
	if err := ValidatePasswordWithMinLength("correct horse battery", 24); err == nil {
		t.Fatal("expected configured minimum length rejection")
	}
	if err := ValidatePasswordWithMinLength("correct horse battery staple", 24); err != nil {
		t.Fatalf("expected password to satisfy configured minimum: %v", err)
	}
}

func TestValidatePasswordAllowsConfiguredMinimumOfEight(t *testing.T) {
	if err := ValidatePasswordWithMinLength("safe-8-x", 8); err != nil {
		t.Fatalf("expected eight-character password to satisfy configured minimum: %v", err)
	}
	if err := ValidatePasswordWithMinLength("short-7", 7); err == nil {
		t.Fatal("expected effective minimum of eight characters")
	}
}

func TestMaskSecret(t *testing.T) {
	if got := MaskSecret("super-secret-token"); got != "<configured>" {
		t.Fatalf("unexpected mask: %s", got)
	}
}

func TestPermissionFailsClosed(t *testing.T) {
	if HasPermission([]string{"streams.read"}, "streams.start") {
		t.Fatal("missing permission should deny")
	}
	if !HasPermission([]string{"streams.start"}, "streams.start") {
		t.Fatal("explicit permission should allow")
	}
}

func TestTokenHash(t *testing.T) {
	token, err := RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	hash := HashToken(token)
	if hash == token {
		t.Fatal("token hash must not equal raw token")
	}
	if !VerifyTokenHash(token, hash) {
		t.Fatal("expected token verification to pass")
	}
	if VerifyTokenHash("wrong", hash) {
		t.Fatal("expected wrong token to fail")
	}
}

func TestDefaultPermissionsExcludeRawSecretRead(t *testing.T) {
	for _, permission := range DefaultPermissions {
		if permission == "secrets.read_raw" {
			t.Fatal("raw secret read permission must not exist")
		}
	}
}
