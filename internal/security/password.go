package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const hashPrefix = "$argon2id$v=19$m=65536,t=3,p=2$"

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return hashPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(sum), nil
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func ValidatePassword(password string) error {
	return ValidatePasswordWithMinLength(password, 12)
}

func ValidatePasswordWithMinLength(password string, minimum int) error {
	if minimum < 12 {
		minimum = 12
	}
	if len(password) < minimum {
		return fmt.Errorf("password must be at least %d characters", minimum)
	}
	weak := []string{"password", "password1234", "123456789012", "adminadminadmin", "change_me_please"}
	for _, w := range weak {
		if strings.EqualFold(password, w) {
			return fmt.Errorf("password is too weak")
		}
	}
	return nil
}

func MaskSecret(value string) string {
	if value == "" {
		return ""
	}
	return "<configured>"
}

func EncryptSecret(value, keyMaterial string) (ciphertext, nonce string, err error) {
	key := sha256.Sum256([]byte(keyMaterial))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonceBytes := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", "", err
	}
	ciphertextBytes := gcm.Seal(nil, nonceBytes, []byte(value), nil)
	return base64.RawStdEncoding.EncodeToString(ciphertextBytes), base64.RawStdEncoding.EncodeToString(nonceBytes), nil
}

func DecryptSecret(ciphertext, nonce, keyMaterial string) (string, error) {
	key := sha256.Sum256([]byte(keyMaterial))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ciphertextBytes, err := base64.RawStdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	nonceBytes, err := base64.RawStdEncoding.DecodeString(nonce)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonceBytes, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func SecretFingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func HasPermission(grants []string, required string) bool {
	for _, grant := range grants {
		if grant == required {
			return true
		}
	}
	return false
}

func RandomToken(byteLen int) (string, error) {
	token := make([]byte, byteLen)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func VerifyTokenHash(raw, hash string) bool {
	got := HashToken(raw)
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == 1
}
